#!/usr/bin/python3 -I
# tw-tenant-request: the only program a tw tenant account may run on the
# relay. Installed and overwritten by tw (tw relay sync-tenants); do not edit.
#
# sshd forces it for every session of a tenant account (command= in the
# account's authorized_keys and ForceCommand in the tw sshd drop-in), so any
# exec, shell or subsystem request the tenant makes ends up here. It reads one
# request line from stdin, hands it to the root handler on this account's own
# socket (/run/tw/tenant/<account>.sock, mode 0600, owned by the account) and
# prints the reply. SSH_ORIGINAL_COMMAND is deliberately ignored: requests
# travel only on stdin, so there is a single input path, it is validated again
# by the handler, and nothing a tenant sends is ever parsed by a shell.

import os
import pwd
import signal
import socket
import sys

SOCKET_DIR = "/run/tw/tenant"
MAX_REQUEST = 128
MAX_REPLY = 1 << 20
# Time the tenant gets to send its request line, and the handler to reply.
READ_TIMEOUT = 15
TIMEOUT = 60


def account():
    return pwd.getpwuid(os.getuid()).pw_name


def read_timed_out(_signum, _frame):
    print("err timed out waiting for the request line on stdin", flush=True)
    os._exit(1)


def main():
    signal.signal(signal.SIGALRM, read_timed_out)
    signal.alarm(READ_TIMEOUT)
    line = sys.stdin.buffer.readline(MAX_REQUEST + 1)
    signal.alarm(0)
    if len(line) > MAX_REQUEST or not line.endswith(b"\n"):
        print("err request must be one newline-terminated line of at most %d bytes" % MAX_REQUEST)
        return 1
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(TIMEOUT)
    reply = b""
    try:
        s.connect(os.path.join(SOCKET_DIR, account() + ".sock"))
        s.sendall(line)
        s.shutdown(socket.SHUT_WR)
        while len(reply) <= MAX_REPLY:
            chunk = s.recv(65536)
            if not chunk:
                break
            reply += chunk
    except OSError as e:
        print("err tenant request channel unavailable on the relay: %s" % (e.strerror or e))
        return 1
    finally:
        s.close()
    sys.stdout.buffer.write(reply[:MAX_REPLY])
    sys.stdout.flush()
    return 0 if reply.startswith(b"ok") else 1


if __name__ == "__main__":
    sys.exit(main())
