//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Relay-side paths of the per-tenant lockdown (internal/relay/tenant).
const (
	tenantRequestPath = "/usr/local/lib/tw/tw-tenant-request"
	tenantSocketDir   = "/run/tw/tenant"
	tenantMapDir      = "/etc/tw/tenants"
	tenantHomeBase    = "/var/lib/tw/tenants"
	// tenantKeyOnRelay is where the tenant-key probes stage a copy of an
	// enrolled server's own SSH private key on the relay, so they can log in
	// exactly as that tenant over loopback — the only source its from="127.0.0.1"
	// pin admits, i.e. the address the VLESS tunnel egresses at.
	tenantKeyOnRelay = "/root/tw-e2e-tenant.key"
	// tenantSSH is plain OpenSSH on the relay, non-interactive, no host-key pinning.
	tenantSSH = "ssh -i " + tenantKeyOnRelay + " -o BatchMode=yes -o StrictHostKeyChecking=no " +
		"-o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR"
)

// expectedTenantAccount is tenant.UserName re-derived independently here:
// "twt-" + the first 24 hex digits of sha256(server id).
func expectedTenantAccount(serverID string) string {
	sum := sha256.Sum256([]byte(serverID))
	return "twt-" + hex.EncodeToString(sum[:])[:24]
}

// serverIDOf reads a server container's server-id from the CN of its own
// relay client cert (the same source CrossTenantCert uses).
func serverIDOf(t *testing.T, service string) string {
	t.Helper()
	subj := execIn(t, service, "openssl x509 -in /etc/tw-test/client.crt -noout -subject 2>/dev/null")
	m := regexp.MustCompile(`CN\s*=\s*([a-z0-9][a-z0-9-]*)`).FindStringSubmatch(subj)
	if m == nil {
		fatalf(t, "could not read %s's server-id from its client cert subject: %q", service, subj)
	}
	return m[1]
}

// relaySSHUserOf returns the relay account stored in a container's live profile.
func relaySSHUserOf(t *testing.T, service string) string {
	t.Helper()
	return strings.TrimSpace(execIn(t, service, `awk '$1=="relay_ssh_user:"{print $2}' /etc/tw-test/config.yaml`))
}

// relayInboundClients returns the relay Xray config's inbounds as
// tag -> client ids, read straight from /usr/local/etc/xray/config.json.
func relayInboundClients(t *testing.T) map[string][]string {
	t.Helper()
	out := execIn(t, "relay", `python3 -I -c '
import json
c = json.load(open("/usr/local/etc/xray/config.json"))
for i in c.get("inbounds", []):
    s = i.get("settings") or {}
    ids = [x.get("id", "") for x in s.get("clients", []) if isinstance(x, dict)]
    print("INBOUND", i.get("tag", "-"), *ids)
'`)
	inbounds := map[string][]string{}
	for _, l := range splitLines(out) {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "INBOUND" {
			inbounds[f[1]] = f[2:]
		}
	}
	return inbounds
}

// inboundsHolding lists the inbound tags whose client list contains uuid.
func inboundsHolding(inbounds map[string][]string, uuid string) []string {
	var tags []string
	for tag, ids := range inbounds {
		for _, id := range ids {
			if id == uuid {
				tags = append(tags, tag)
				break
			}
		}
	}
	return tags
}

// userUUIDOn returns a user's Xray UUID from `tw server user list` on service.
func userUUIDOn(t *testing.T, service, user string) string {
	t.Helper()
	out := execIn(t, service, "tw server user list")
	lines := splitLines(out)
	for i, l := range lines {
		if strings.TrimSpace(l) != user {
			continue
		}
		for _, next := range lines[i+1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(next), "UUID: "); ok {
				return v
			}
			if !strings.HasPrefix(next, "    ") {
				break
			}
		}
	}
	fatalf(t, "no UUID for user %q in tw server user list on %s:\n%s", user, service, out)
	return ""
}

// stageTenantKey copies service's own SSH private key to the relay (root-only)
// for the loopback tenant-login probes. The caller removes it.
func stageTenantKey(t *testing.T, service string) {
	t.Helper()
	b64 := strings.TrimSpace(execIn(t, service, "base64 -w0 /etc/tw-test/id_ed25519"))
	execIn(t, "relay", "umask 077 && printf '%s' '"+b64+"' | base64 -d > "+tenantKeyOnRelay)
}

// tenantRequestAs sends one request line to the tenant handler on the relay
// as account, through the same forced command an SSH login of that account
// runs, without needing its key.
func tenantRequestAs(account, line string) (string, error) {
	return execInOK("relay", "printf '"+line+"\\n' | runuser -u "+account+" -- "+tenantRequestPath+" 2>&1")
}

// assertTenantAccountLockedDown checks the relay-side shape of one tenant
// account: own group only (no sudo), sudo refuses it, root-owned
// authorized_keys forcing tw-tenant-request, its server-id mapping and its
// socket.
func assertTenantAccountLockedDown(t *testing.T, account string) {
	t.Helper()
	pw := strings.TrimSpace(execIn(t, "relay", "getent passwd "+account))
	if f := strings.Split(pw, ":"); len(f) < 7 || f[5] != tenantHomeBase+"/"+account {
		fatalf(t, "tenant account %s has an unexpected passwd entry: %q", account, pw)
	}
	if groups := strings.TrimSpace(execIn(t, "relay", "id -nG "+account)); groups != account {
		fatalf(t, "tenant account %s is in groups %q, want only its own group (no sudo)", account, groups)
	}
	sudoOut, _ := execInOK("relay", "sudo -l -U "+account+" 2>&1")
	if !strings.Contains(sudoOut, "not allowed to run sudo") {
		fatalf(t, "sudo -l -U %s does not report the tenant as not allowed to run sudo:\n%s", account, sudoOut)
	}
	ak := tenantHomeBase + "/" + account + "/.ssh/authorized_keys"
	if owner := strings.TrimSpace(execIn(t, "relay", "stat -c '%U %a' "+ak)); owner != "root 644" {
		fatalf(t, "%s is %q, want root-owned 644 (the tenant must not be able to rewrite its own key options)", ak, owner)
	}
	akLine := strings.TrimSpace(execIn(t, "relay", "cat "+ak))
	if len(splitLines(akLine)) != 1 || !strings.Contains(akLine, `command="`+tenantRequestPath+`"`) ||
		!strings.Contains(akLine, `from="127.0.0.1"`) || !strings.Contains(akLine, `permitopen="127.0.0.1:1"`) {
		fatalf(t, "%s is not a single forced-command, loopback-pinned, permitopen-sentinel line:\n%s", ak, akLine)
	}
	execIn(t, "relay", "test -s "+tenantMapDir+"/"+account)
	if sock := strings.TrimSpace(execIn(t, "relay", "stat -c '%U %a' "+tenantSocketDir+"/"+account+".sock")); sock != account+" 600" {
		fatalf(t, "tenant socket of %s is %q, want owned by the tenant and mode 600", account, sock)
	}
}

// testTenantLockdown is the C1 regression on the real relay: an enrolled
// server (a tenant) no longer logs in as the relay admin (NOPASSWD sudo) but
// as its own twt-* account, which can only send add/remove/list requests about
// its own Xray inbound to a root handler — never run a command.
func testTenantLockdown(t *testing.T) {
	scenario(t, "an enrolled server's relay login is a locked-down per-tenant account that cannot run anything on the relay (C1)",
		"server-1's stored relay_ssh_user is twt-<24 hex of sha256(server id)>, and that account exists on the relay with only its own group, no sudo (sudo -l refuses it), a root-owned forced-command authorized_keys and its own 0600 request socket",
		"the relay admin account's authorized_keys holds exactly one key (the admin's) — server-1's key is not in it",
		"logging in with server-1's own key over loopback: an exec request ('id; echo …'), a shell fed through stdin, 'sudo -n id' and an sftp subsystem request all land in tw-tenant-request instead — no command output, only its 'err …' reply",
		"a -W stdio forward to the relay's Xray gRPC API (127.0.0.1:10085) is administratively prohibited",
		"the same login's 'list' request returns the tenant's own inbound clients, including alice's UUID; a malformed UUID is refused by the handler",
		"alice's UUID sits in server-1's own vless-in-<server id> inbound and in no other inbound",
		"root connecting to the tenant's socket is refused by the handler (the peer uid must own the socket)")

	sid := serverIDOf(t, "server")
	account := relaySSHUserOf(t, "server")
	if want := expectedTenantAccount(sid); account != want {
		fatalf(t, "server-1 (server id %s) stores relay_ssh_user %q, want its tenant account %q", sid, account, want)
	}
	assertTenantAccountLockedDown(t, account)

	// The admin account (NOPASSWD sudo) carries the admin key only.
	adminAK := strings.TrimSpace(execIn(t, "relay", "cat /home/*/.ssh/authorized_keys"))
	serverKey := strings.Fields(execIn(t, "server", "cat /etc/tw-test/id_ed25519.pub"))
	if len(serverKey) < 2 {
		fatalf(t, "unreadable server-1 public key: %v", serverKey)
	}
	if n := len(splitLines(adminAK)); n != 1 || strings.Contains(adminAK, serverKey[1]) {
		fatalf(t, "relay admin authorized_keys must hold exactly the admin key (got %d lines, server-1 key present: %v):\n%s",
			n, strings.Contains(adminAK, serverKey[1]), adminAK)
	}

	stageTenantKey(t, "server")
	defer execIn(t, "relay", "rm -f "+tenantKeyOnRelay)
	login := tenantSSH + " " + account + "@127.0.0.1"

	// Every way of asking for a command ends in the forced tw-tenant-request.
	for _, probe := range []struct{ label, cmd, marker string }{
		{"exec request", login + ` 'id; echo TW_TENANT_EXEC_RAN' < /dev/null`, "TW_TENANT_EXEC_RAN"},
		{"shell via stdin", `printf 'id\necho TW_TENANT_SHELL_RAN\n' | ` + tenantSSH + " -T " + account + "@127.0.0.1", "TW_TENANT_SHELL_RAN"},
		{"sudo exec", login + ` 'sudo -n id -u; echo TW_TENANT_SUDO_RAN' < /dev/null`, "TW_TENANT_SUDO_RAN"},
		{"sftp subsystem", tenantSSH + " -s " + account + "@127.0.0.1 sftp < /dev/null", "TW_NEVER_PRINTED"},
	} {
		out, err := execInOK("relay", probe.cmd+" 2>&1")
		if err == nil {
			fatalf(t, "tenant %s exited 0 (expected the forced command's refusal):\n%s", probe.label, out)
		}
		if strings.Contains(out, probe.marker) || strings.Contains(out, "uid=") {
			fatalf(t, "tenant %s RAN on the relay (C1 is back):\n%s", probe.label, out)
		}
		if !strings.Contains(out, "err ") {
			fatalf(t, "tenant %s did not get tw-tenant-request's 'err …' reply (something else answered):\n%s", probe.label, out)
		}
		t.Logf("tenant %s refused: %s", probe.label, strings.TrimSpace(out))
	}

	// Local forwarding is pinned to a dead sentinel: no path to the Xray API.
	// At LogLevel=INFO ssh prints the server's refusal reason (ssh keeps the
	// first value of an option, so swap it rather than append it).
	verbose := strings.Replace(tenantSSH, "LogLevel=ERROR", "LogLevel=INFO", 1)
	if out, err := execInOK("relay", verbose+" -W 127.0.0.1:10085 "+account+"@127.0.0.1 < /dev/null 2>&1"); err == nil || !strings.Contains(out, "administratively prohibited") {
		fatalf(t, "tenant -W 127.0.0.1:10085 was not administratively prohibited (err=%v):\n%s", err, out)
	}

	// The legitimate channel works over the same login and sees only its own inbound.
	aliceUUID := userUUIDOn(t, "server", "alice")
	listOut, err := execInOK("relay", "printf 'list\\n' | "+login+" 2>&1")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(listOut), "ok") || !strings.Contains(listOut, aliceUUID) {
		fatalf(t, "tenant 'list' over its own login failed or misses alice's UUID %s (err=%v):\n%s", aliceUUID, err, listOut)
	}
	badOut, err := execInOK("relay", "printf 'add ../../etc/passwd\\n' | "+login+" 2>&1")
	if err == nil || !strings.Contains(badOut, "err uuid must be a canonical lowercase UUID") {
		fatalf(t, "tenant 'add' with a malformed UUID was not refused by the handler (err=%v):\n%s", err, badOut)
	}

	inbounds := relayInboundClients(t)
	if tags := inboundsHolding(inbounds, aliceUUID); len(tags) != 1 || tags[0] != "vless-in-"+sid {
		fatalf(t, "alice's UUID %s is in inbounds %v, want exactly [vless-in-%s]:\n%v", aliceUUID, tags, sid, inbounds)
	}

	// Identity is the peer uid, not who can reach the path: root is refused.
	rootOut := execIn(t, "relay", `python3 -I -c '
import socket
s = socket.socket(socket.AF_UNIX)
s.connect("`+tenantSocketDir+"/"+account+`.sock")
s.sendall(b"list\n")
print(s.recv(4096).decode())
'`)
	if !strings.Contains(rootOut, "err peer account does not own this socket") {
		fatalf(t, "root's request on %s's socket was not refused by the peer-uid check:\n%s", account, rootOut)
	}
}

// assertCrossTenantSocketIsolation is SecondTenant's C1 isolation check while
// both servers are enrolled: server2's tenant account can neither connect to
// server-1's request socket nor see server-1's clients through its own.
func assertCrossTenantSocketIsolation(t *testing.T, server1Account, server2Account, server2ID, server1UserUUID string) {
	t.Helper()
	assertTenantAccountLockedDown(t, server2Account)
	if server1Account == server2Account {
		fatalf(t, "server-1 and server2 share tenant account %s", server1Account)
	}
	out, err := execInOK("relay", "runuser -u "+server2Account+` -- python3 -I -c '
import socket
s = socket.socket(socket.AF_UNIX)
s.connect("`+tenantSocketDir+"/"+server1Account+`.sock")
s.sendall(b"remove `+server1UserUUID+`\n")
print("TW_CROSS_TENANT_CONNECTED", s.recv(4096).decode())
' 2>&1`)
	if err == nil || strings.Contains(out, "TW_CROSS_TENANT_CONNECTED") || !strings.Contains(out, "Permission denied") {
		fatalf(t, "server2's tenant account reached server-1's request socket (err=%v):\n%s", err, out)
	}
	own, err := tenantRequestAs(server2Account, "list")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(own), "ok") {
		fatalf(t, "server2's own tenant 'list' failed (err=%v):\n%s", err, own)
	}
	if strings.Contains(own, server1UserUUID) {
		fatalf(t, "server2's tenant 'list' shows server-1's user %s — not confined to vless-in-%s:\n%s", server1UserUUID, server2ID, own)
	}
	// A remove of server-1's user through server2's own channel targets only
	// server2's inbound: absent there, and server-1's inbound keeps it.
	rm, err := tenantRequestAs(server2Account, "remove "+server1UserUUID)
	if err != nil || strings.TrimSpace(rm) != "ok absent" {
		fatalf(t, "server2's 'remove <server-1 user>' did not answer 'ok absent' (err=%v):\n%s", err, rm)
	}
	if tags := inboundsHolding(relayInboundClients(t), server1UserUUID); len(tags) != 1 || tags[0] == "vless-in-"+server2ID {
		fatalf(t, "server-1's user %s is in inbounds %v after server2's remove request", server1UserUUID, tags)
	}
}

// assertTenantAccountGone checks un-enroll removed a tenant's relay account.
func assertTenantAccountGone(t *testing.T, account string) {
	t.Helper()
	out := execIn(t, "relay", "getent passwd "+account+" || echo GONE; "+
		"test -e "+tenantSocketDir+"/"+account+".sock && echo SOCKET-LEFT; "+
		"test -e "+tenantMapDir+"/"+account+" && echo MAP-LEFT; "+
		"test -e "+tenantHomeBase+"/"+account+" && echo HOME-LEFT; true")
	if strings.TrimSpace(out) != "GONE" {
		fatalf(t, "tenant account %s not fully removed after un-enroll:\n%s", account, out)
	}
}

// listenerUID returns the uid of the relay process holding a LISTEN socket
// on 127.0.0.1:port, or "" when nothing listens there.
func listenerUID(port int) string {
	out, _ := execInOK("relay", fmt.Sprintf(
		`pid=$(ss -ltnpH 'sport = :%d' | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | head -1); `+
			`[ -n "$pid" ] && awk '/^Uid:/{print $2}' /proc/$pid/status`, port))
	return strings.TrimSpace(out)
}

// testTenantMigration covers `tw relay sync-tenants` on a relay in the
// pre-fix layout: the enrolled server's key sits in the admin account's
// authorized_keys and its profile logs in as the admin. The sync must move it
// to its own tenant account, end the pre-migration session, and the server
// daemon must re-attach as the tenant on its own.
func testTenantMigration(t *testing.T) {
	scenario(t, "tw relay sync-tenants migrates a pre-fix relay: tenant key out of the admin account, into its own locked-down account (C1 migration)",
		"tw relay sync-tenants on an in-sync relay is a no-op that exits 0",
		"fixture: server-1's tenant account is removed and its key is appended to the relay admin's authorized_keys in the pre-fix format; server-1's profile logs in as the admin; its restarted daemon's reverse tunnel listener runs as the admin uid — and the key can run commands as the admin there (the C1 state)",
		"tw relay sync-tenants reports the relay in sync; the admin authorized_keys is back to the admin key only and the key can no longer log in as the admin",
		"server-1's tenant account is recreated locked down, and the pre-migration session holding its tunnel port is ended",
		"server-1's running daemon falls back to its tenant account on its own: its tunnel listener comes back under the tenant uid, relay_ssh_user in its profile is rewritten to the tenant account, and tw server test reports 'tunnel and tenant request channel working'")

	out := execIn(t, "admin", "tw relay sync-tenants")
	if !strings.Contains(out, "in sync with the registry") {
		fatalf(t, "tw relay sync-tenants on an in-sync relay did not report success:\n%s", out)
	}

	sid := serverIDOf(t, "server")
	account := expectedTenantAccount(sid)
	adminUser := relaySSHUserOf(t, "admin")
	serverHost := strings.TrimSpace(execIn(t, "server", "hostname"))
	row := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sid) + `\s+/tw/\S+\s+(\d+)\s`).FindStringSubmatch(execIn(t, "admin", "tw relay get-servers"))
	if row == nil {
		fatalf(t, "tw relay get-servers has no row for server-1 (%s, host %s)", sid, serverHost)
	}
	port, _ := strconv.Atoi(row[1])
	adminUID := strings.TrimSpace(execIn(t, "relay", "id -u "+adminUser))
	pub := strings.TrimSpace(execIn(t, "server", "cat /etc/tw-test/id_ed25519.pub"))
	keyFields := strings.Fields(pub)
	if len(keyFields) < 2 {
		fatalf(t, "unreadable server-1 public key: %q", pub)
	}

	// --- Fixture: the pre-fix relay layout ---
	killMatching(t, "server", "tw server start")
	execIn(t, "relay", "systemctl disable --now tw-tenant@"+account+".socket; "+
		"pkill -KILL -u "+account+"; sleep 1; userdel "+account+" && "+
		"rm -rf "+tenantHomeBase+"/"+account+" "+tenantMapDir+"/"+account+" "+tenantSocketDir+"/"+account+".sock")
	legacy := fmt.Sprintf(`from="127.0.0.1",restrict,port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:%d",permitlisten="127.0.0.1:%d" %s %s`,
		port, port+20000, keyFields[0], keyFields[1])
	execIn(t, "relay", "printf '%s\\n' '"+legacy+"' >> /home/"+adminUser+"/.ssh/authorized_keys")
	execIn(t, "server", `sed -i 's/^\(\s*relay_ssh_user:\).*/\1 `+adminUser+`/' /etc/tw-test/config.yaml`)
	if got := relaySSHUserOf(t, "server"); got != adminUser {
		fatalf(t, "fixture: server-1 relay_ssh_user is %q, want the admin %q", got, adminUser)
	}
	execDetached(t, "server", "tw server start >> /var/log/tw-server.log 2>&1")
	waitFor(t, "pre-migration tunnel listener under the admin uid", 90*time.Second, func() (bool, string) {
		uid := listenerUID(port)
		return uid == adminUID, "listener uid: " + uid
	})

	stageTenantKey(t, "server")
	defer execIn(t, "relay", "rm -f "+tenantKeyOnRelay)
	asAdmin := tenantSSH + " " + adminUser + "@127.0.0.1 'echo TW_LEGACY_EXEC_RAN' < /dev/null 2>&1"
	if out, err := execInOK("relay", asAdmin); err != nil || !strings.Contains(out, "TW_LEGACY_EXEC_RAN") {
		fatalf(t, "fixture is not the pre-fix state: server-1's key could not run a command as the admin (err=%v):\n%s", err, out)
	}

	// --- Migrate ---
	out = execIn(t, "admin", "tw relay sync-tenants")
	if !strings.Contains(out, "in sync with the registry") {
		fatalf(t, "tw relay sync-tenants did not report success:\n%s", out)
	}
	adminAK := strings.TrimSpace(execIn(t, "relay", "cat /home/"+adminUser+"/.ssh/authorized_keys"))
	if len(splitLines(adminAK)) != 1 || strings.Contains(adminAK, keyFields[1]) {
		fatalf(t, "admin authorized_keys still carries server-1's key after sync-tenants:\n%s", adminAK)
	}
	if out, err := execInOK("relay", asAdmin); err == nil || strings.Contains(out, "TW_LEGACY_EXEC_RAN") {
		fatalf(t, "server-1's key still runs commands as the admin after sync-tenants (err=%v):\n%s", err, out)
	}
	assertTenantAccountLockedDown(t, account)
	tenantUID := strings.TrimSpace(execIn(t, "relay", "id -u "+account))

	// The running daemon re-attaches through its tenant account by itself.
	waitFor(t, "tunnel listener back under the tenant uid", 120*time.Second, func() (bool, string) {
		uid := listenerUID(port)
		return uid == tenantUID, "listener uid: " + uid
	})
	waitFor(t, "server-1 adopts its tenant account", 60*time.Second, func() (bool, string) {
		got := relaySSHUserOf(t, "server")
		return got == account, "relay_ssh_user: " + got
	})
	waitFor(t, "server-1 tunnel and tenant channel after migration", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and tenant request channel working"), out
	})
}
