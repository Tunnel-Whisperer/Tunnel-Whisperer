#!/usr/bin/python3 -I
# tw-tenant-handler: the root side of a tenant request on a tw relay.
# Installed and overwritten by tw (tw relay sync-tenants); do not edit.
#
# systemd (tw-tenant@.socket, Accept=yes) starts one instance per connection
# on /run/tw/tenant/<account>.sock with the connection as stdin/stdout. The
# tenant is identified only by facts the kernel and systemd vouch for: the
# path of the socket the connection arrived on, and the peer's uid
# (SO_PEERCRED). Both must name the same tenant account. That account maps to
# a server id through a root-owned file the relay admin wrote at enrollment
# (/etc/tw/tenants/<account>). The request line only picks the operation and
# a UUID; it can never name an inbound, a file or another tenant.
#
# Request (one line): "add <uuid>", "remove <uuid>" or "list".
# Reply (one line):   "ok ..." or "err <reason>"; exit status 1 on "err".
# A change is saved to config.json first, then applied to the running Xray
# through its API. The handler never restarts Xray: when the API call fails
# the reply is "ok <state>, saved; live apply failed (<reason>), takes effect
# on next xray restart" and tw reports that to the operator.

import copy
import fcntl
import hashlib
import json
import os
import pwd
import re
import socket
import stat
import struct
import subprocess
import sys
import tempfile
import time

SOCKET_DIR = "/run/tw/tenant"
MAP_DIR = "/etc/tw/tenants"
MAP_OWNER_UID = 0
XRAY_CONFIG = "/usr/local/etc/xray/config.json"
LOCK_PATH = "/usr/local/etc/xray/.tw-config.lock"
XRAY_BIN = "/usr/local/bin/xray"
XRAY_API = "127.0.0.1:10085"
MAX_REQUEST = 128
# Total time a peer gets to send its request line.
IO_TIMEOUT = 15
CMD_TIMEOUT = 20
# Most clients one tenant inbound may hold (the server's own id included).
MAX_CLIENTS = 1000
LIVE_FAILED = "live apply failed"

USER_RE = re.compile(r"twt-[0-9a-f]{24}")
SERVER_ID_RE = re.compile(r"[a-z0-9][a-z0-9-]{0,63}")
# Same rule as isCanonicalUUID in tw (internal/ops/uuidcheck.go).
UUID_RE = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")


class Refused(Exception):
    """A request failure whose message is safe to send back to the tenant."""


def tenant_user(server_id):
    """Must match tenant.UserName in tw (internal/relay/tenant)."""
    return "twt-" + hashlib.sha256(server_id.encode("ascii")).hexdigest()[:24]


def user_for_uid(uid):
    return pwd.getpwuid(uid).pw_name


def server_id_for(user):
    path = os.path.join(MAP_DIR, user)
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    except FileNotFoundError:
        raise Refused("this account is not registered as a tenant on the relay")
    try:
        st = os.fstat(fd)
        if (not stat.S_ISREG(st.st_mode) or st.st_uid != MAP_OWNER_UID
                or st.st_mode & 0o022):
            raise Refused("tenant registration on the relay is not trustworthy")
        data = os.read(fd, 129)
    finally:
        os.close(fd)
    try:
        text = data.decode("ascii")
    except UnicodeDecodeError:
        raise Refused("tenant registration on the relay is malformed")
    if not text.endswith("\n"):
        raise Refused("tenant registration on the relay is malformed")
    server_id = text[:-1]
    if not SERVER_ID_RE.fullmatch(server_id) or tenant_user(server_id) != user:
        raise Refused("tenant registration does not match this account")
    return server_id


def identify(sock):
    """Return (account, server_id) for the peer of sock, or raise Refused."""
    name = sock.getsockname()
    if not isinstance(name, str):
        raise Refused("connection did not arrive on a tenant socket")
    directory, base = os.path.split(name)
    user = base[:-len(".sock")] if base.endswith(".sock") else ""
    if directory != SOCKET_DIR or not USER_RE.fullmatch(user):
        raise Refused("connection did not arrive on a tenant socket")
    creds = sock.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED,
                            struct.calcsize("3i"))
    _pid, uid, _gid = struct.unpack("3i", creds)
    try:
        peer = user_for_uid(uid)
    except KeyError:
        raise Refused("peer account is unknown")
    if peer != user:
        raise Refused("peer account does not own this socket")
    return user, server_id_for(user)


def read_request(sock):
    deadline = time.monotonic() + IO_TIMEOUT
    buf = b""
    while b"\n" not in buf:
        left = deadline - time.monotonic()
        if left <= 0:
            raise Refused("timed out waiting for the request")
        sock.settimeout(left)
        try:
            chunk = sock.recv(MAX_REQUEST + 1 - len(buf))
        except socket.timeout:
            raise Refused("timed out waiting for the request")
        if not chunk:
            break
        buf += chunk
        if len(buf) > MAX_REQUEST:
            raise Refused("request too long")
    line, newline, rest = buf.partition(b"\n")
    if not newline:
        raise Refused("request must be one newline-terminated line")
    if rest:
        raise Refused("only one request per connection")
    try:
        fields = line.decode("ascii").split(" ")
    except UnicodeDecodeError:
        raise Refused("request must be ASCII")
    if fields == ["list"]:
        return "list", None
    if len(fields) == 2 and fields[0] in ("add", "remove"):
        if not UUID_RE.fullmatch(fields[1]):
            raise Refused("uuid must be a canonical lowercase UUID")
        return fields[0], fields[1]
    raise Refused("unknown request (expected: add <uuid> | remove <uuid> | list)")


def load_config():
    with open(XRAY_CONFIG, "rb") as f:
        return json.loads(f.read())


def save_config(conf):
    """Atomically replace the Xray config, keeping its mode and owner."""
    st = os.stat(XRAY_CONFIG)
    fd, tmp = tempfile.mkstemp(prefix=".config.json.tw-",
                               dir=os.path.dirname(XRAY_CONFIG))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(conf, f, indent=2)
            f.write("\n")
            f.flush()
            os.fchown(f.fileno(), st.st_uid, st.st_gid)
            os.fchmod(f.fileno(), stat.S_IMODE(st.st_mode))
            os.fsync(f.fileno())
        os.replace(tmp, XRAY_CONFIG)
    except BaseException:
        try:
            os.unlink(tmp)
        except FileNotFoundError:
            pass
        raise


def tenant_inbound(conf, tag):
    inbounds = conf.get("inbounds") if isinstance(conf, dict) else None
    if not isinstance(inbounds, list):
        raise Refused("relay xray config has no inbounds")
    matches = [ib for ib in inbounds if isinstance(ib, dict) and ib.get("tag") == tag]
    if len(matches) != 1:
        raise Refused("inbound %s not found on the relay" % tag)
    settings = matches[0].get("settings")
    if not isinstance(settings, dict) or not isinstance(settings.get("clients", []), list):
        raise Refused("inbound %s is malformed on the relay" % tag)
    settings.setdefault("clients", [])
    return matches[0]


def client_ids(inbound):
    return [c["id"] for c in inbound["settings"]["clients"]
            if isinstance(c, dict) and isinstance(c.get("id"), str)]


def run(argv, stdin=None):
    try:
        r = subprocess.run(argv, input=stdin, stdout=subprocess.PIPE,
                           stderr=subprocess.STDOUT, text=True, timeout=CMD_TIMEOUT)
    except (OSError, subprocess.TimeoutExpired) as e:
        return False, repr(e)
    return r.returncode == 0, r.stdout


class LiveResult:
    """Outcome of one `xray api adu|rmu` call for a single user."""

    def __init__(self, applied, reason=""):
        self.applied = applied
        self.reason = reason


# `xray api adu|rmu` print one line per failed user,
#   rpc error: code = <code> desc = <xray error>
# and exit 0 whatever happened; only the summary line and these error lines
# tell the outcome, so both are matched exactly.
RPC_ERROR = r"rpc error: code = \w+ desc = "


def api_outcome(ok, out, summary_ok, benign_re, tag):
    if not ok:
        log("xray api call failed: %s" % out.strip().replace("\n", " | "))
        return LiveResult(False, "xray api unreachable")
    lines = [l.strip() for l in out.splitlines() if l.strip()]
    if lines and lines[-1] == summary_ok:
        return LiveResult(True)
    if any(re.fullmatch(benign_re, l) for l in lines):
        return LiveResult(True)
    if any(re.fullmatch(RPC_ERROR + r"(?:.* > )?(?:[\w/]+: )?handler not found: " + re.escape(tag), l)
           for l in lines):
        raise Refused("inbound %s is not loaded in the relay's xray" % tag)
    log("xray api call failed: %s" % out.strip().replace("\n", " | "))
    return LiveResult(False, "xray api rejected the change")


def api_add(inbound, uuid):
    one = copy.deepcopy(inbound)
    one["settings"]["clients"] = [{"id": uuid, "email": uuid}]
    ok, out = run([XRAY_BIN, "api", "adu", "-s", XRAY_API, "stdin:"],
                  json.dumps({"inbounds": [one]}))
    return api_outcome(ok, out, "Added 1 user(s) in total.",
                       RPC_ERROR + r"(?:[\w/]+: )?User " + re.escape(uuid) + r" already exists\.",
                       inbound["tag"])


def api_remove(tag, uuid):
    ok, out = run([XRAY_BIN, "api", "rmu", "-s", XRAY_API, "-tag=" + tag, uuid])
    return api_outcome(ok, out, "Removed 1 user(s) in total.",
                       RPC_ERROR + r"(?:[\w/]+: )?User " + re.escape(uuid) + r" not found\.",
                       tag)


def describe(state, live):
    if live.applied:
        return "ok absent" if state == "absent" else "ok %s, applied via xray api" % state
    return "ok %s, saved; %s (%s), takes effect on next xray restart" % (
        state, LIVE_FAILED, live.reason)


def handle(sock):
    user, server_id = identify(sock)
    verb, uuid = read_request(sock)
    tag = "vless-in-" + server_id
    lock = os.open(LOCK_PATH, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        fcntl.flock(lock, fcntl.LOCK_EX)
        # The admin may have un-enrolled this tenant while we waited for the
        # lock; act only on a registration that still holds under it.
        if server_id_for(user) != server_id:
            raise Refused("tenant registration changed on the relay; retry")
        conf = load_config()
        inbound = tenant_inbound(conf, tag)
        clients = inbound["settings"]["clients"]
        ids = client_ids(inbound)
        if verb == "list":
            return user, verb, " ".join(["ok"] + ids)
        if verb == "add":
            state = "present"
            if uuid not in ids:
                if len(clients) >= MAX_CLIENTS:
                    raise Refused("inbound %s already holds %d clients, the most a tenant may have"
                                  % (tag, MAX_CLIENTS))
                clients.append({"id": uuid, "email": uuid})
                save_config(conf)
                state = "added"
            return user, verb, describe(state, api_add(inbound, uuid))
        # remove
        state = "absent"
        if uuid in ids:
            if any(isinstance(c, dict) and c.get("id") == uuid and c.get("email") != uuid
                   for c in clients):
                raise Refused("refusing to remove the server's own identity")
            inbound["settings"]["clients"] = [
                c for c in clients if not (isinstance(c, dict) and c.get("id") == uuid)]
            save_config(conf)
            state = "removed"
        # Also when absent from the file: a remove whose live part failed
        # earlier is retried by sending it again.
        return user, verb, describe(state, api_remove(tag, uuid))
    finally:
        os.close(lock)


def log(msg):
    print("tw-tenant-handler: " + msg, file=sys.stderr, flush=True)


def main():
    sock = socket.socket(fileno=0)
    user, verb, code = "?", "?", 1
    try:
        user, verb, reply = handle(sock)
        code = 0
    except Refused as e:
        reply = "err " + str(e)
    except Exception as e:  # noqa: BLE001 - report, never leak details to the tenant
        log("internal error: %r" % (e,))
        reply = "err internal error on the relay (see journalctl -u 'tw-tenant@*')"
    log("peer=%s request=%s reply=%s" % (user, verb, reply[:200]))
    try:
        sock.sendall((reply + "\n").encode("ascii", "replace"))
    except OSError as e:
        log("could not send reply: %r" % (e,))
        code = 1
    return code


if __name__ == "__main__":
    sys.exit(main())
