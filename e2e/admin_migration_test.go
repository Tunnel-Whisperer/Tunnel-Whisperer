//go:build e2e

package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

// migPort is AdminMigration's throwaway user's local tunnel port — distinct
// from every other scenario's port.
const migPort = "18094"

// testAdminMigration proves a relay bundle exported on one admin machine
// administers the same relay from a second machine with a different hostname
// (ISS-004): the tenant identity (server_id) and CA travel in config.yaml and
// the bundle, never re-derived from the new hostname, and the bundle carries
// no Terraform cache.
func testAdminMigration(t *testing.T) {
	scenario(t, "a relay bundle exported on admin administers the same relay from admin2 (different hostname) and carries no cache files",
		"admin and admin2 report different hostnames",
		"tw config export -o <dir>/ writes tw_<context>.twctx and -o <file> writes the exact path, both mode 600, with Terraform cache artefacts planted on admin",
		"the exported bundle holds config.yaml, ca.key and relay/manual-relay.json but no relay/.terraform/, relay/terraform.tfstate.backup or cache/ entries, and is under 256 KiB",
		"on a wiped admin2, tw config import --activate makes the relay context current with admin's server_id and identical ca.crt/ca.key/client.crt; tw relay status shows Provisioned: yes and the domain",
		"from admin2, tw relay get-servers lists server1, and tw relay add-server + un-enroll-server both succeed without changing admin2's ca.crt, config path, or the admin tenant's single /tw/<server_id> route in the relay Caddyfile",
		"regression guard: a fresh user's forward from client to server1 still round-trips bytes, and the original admin's tw relay get-servers still lists server1",
		"cleanup: planted artefacts, the throwaway server context/user and the exported bundles are removed; admin2 keeps its relay context")

	// Re-runnability: wipe leftovers from a prior partial run.
	killMatching(t, "admin2", "tw relay add-server")
	killMatching(t, "client", "twmig")
	execIn(t, "admin", "rm -rf /shared/migrate /shared/migrate-addserver.log")
	execIn(t, "client", "rm -rf /tmp/twmig")
	if out, err := execInOK("server", "printf 'y\\n' | tw server user delete miguser"); err == nil {
		t.Logf("pre-cleanup: deleted a leftover miguser from a previous run:\n%s", out)
	}

	// (a) Two genuinely different machines.
	h1 := strings.TrimSpace(execIn(t, "admin", "hostname"))
	h2 := strings.TrimSpace(execIn(t, "admin2", "hostname"))
	if h1 == h2 {
		fatalf(t, "admin and admin2 share hostname %q — the migration would not cross a hostname change", h1)
	}

	// (b) Plant cache artefacts on admin, record its identity, export twice.
	out := execIn(t, "admin", "tw config get-contexts")
	row := regexp.MustCompile(`(?m)^\*\s+(\S+)\s+([0-9a-f]{8})\s+relay\s+`).FindStringSubmatch(out)
	if row == nil {
		fatalf(t, "admin current-context row missing name or 8-hex ID:\n%s", out)
	}
	ctxName, ctxID := row[1], row[2]
	planted := []string{
		"/etc/tw-test/relay/.terraform",
		"/etc/tw-test/relay/terraform.tfstate.backup",
		"/etc/tw-test/cache/terraform/" + ctxID,
	}
	execIn(t, "admin",
		"mkdir -p /etc/tw-test/relay/.terraform/providers/x /etc/tw-test/cache/terraform/"+ctxID+"/providers/x && "+
			"dd if=/dev/zero of=/etc/tw-test/relay/.terraform/providers/x/provider.bin bs=1M count=1 2>/dev/null && "+
			"dd if=/dev/zero of=/etc/tw-test/cache/terraform/"+ctxID+"/providers/x/provider.bin bs=1M count=1 2>/dev/null && "+
			"touch /etc/tw-test/relay/terraform.tfstate.backup")
	defer execIn(t, "admin", "rm -rf "+strings.Join(planted, " "))

	adminHashes := identityHashes(t, "admin")
	adminSID := configLine(t, "admin", "server_id")
	adminPath := configLine(t, "admin", "path")

	execIn(t, "admin", "mkdir -p /shared/migrate")
	defer execIn(t, "admin", "rm -rf /shared/migrate")
	execIn(t, "admin", "tw config export -o /shared/migrate/")
	execIn(t, "admin", "tw config export -o /shared/migrate/relay-b.twctx")
	for _, f := range []string{"/shared/migrate/tw_" + ctxName + ".twctx", "/shared/migrate/relay-b.twctx"} {
		mode := strings.TrimSpace(execIn(t, "admin", "stat -c %a "+f))
		if mode != "600" {
			fatalf(t, "exported bundle %s has mode %s, want 600", f, mode)
		}
	}

	// (c) Inspect the bundle on the host. The file is root-owned 0600, so it
	// is read through the container (base64 keeps the bytes intact).
	b64 := execIn(t, "admin", "base64 -w0 /shared/migrate/relay-b.twctx")
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		fatalf(t, "decoding exported bundle: %v", err)
	}
	if len(data) >= 256*1024 {
		fatalf(t, "exported bundle is %d bytes, want < 256 KiB — cache files leaked in?", len(data))
	}
	plain, err := cryptobox.Decrypt(data, "")
	if err != nil {
		fatalf(t, "decrypting exported bundle: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		fatalf(t, "opening exported bundle zip: %v", err)
	}
	entries := map[string]bool{}
	for _, f := range zr.File {
		entries[f.Name] = true
		if strings.HasPrefix(f.Name, "relay/.terraform/") || strings.HasPrefix(f.Name, "cache/") ||
			f.Name == "relay/terraform.tfstate.backup" {
			fatalf(t, "exported bundle carries cache artefact %q", f.Name)
		}
	}
	for _, want := range []string{"config.yaml", "ca.key", "relay/manual-relay.json"} {
		if !entries[want] {
			fatalf(t, "exported bundle missing %q; entries: %v", want, entries)
		}
	}
	t.Logf("exported bundle: %d bytes, %d entries", len(data), len(entries))

	// (d) Import on a wiped admin2 and compare identity.
	execIn(t, "admin2", "rm -rf /etc/tw-test")
	execIn(t, "admin2", "tw config import /shared/migrate/relay-b.twctx --activate")
	if cur := strings.TrimSpace(execIn(t, "admin2", "tw config current-context")); cur != ctxName {
		fatalf(t, "admin2 current-context = %q after import --activate, want %q", cur, ctxName)
	}
	if sid := configLine(t, "admin2", "server_id"); sid != adminSID {
		fatalf(t, "admin2 server_id %q != admin's %q", sid, adminSID)
	}
	admin2Hashes := identityHashes(t, "admin2")
	if admin2Hashes != adminHashes {
		fatalf(t, "admin2 ca.crt/ca.key/client.crt hashes differ from admin's:\nadmin:\n%s\nadmin2:\n%s", adminHashes, admin2Hashes)
	}
	statusOut := execIn(t, "admin2", "tw relay status")
	if !strings.Contains(statusOut, "Provisioned: yes") || !strings.Contains(statusOut, domain) {
		fatalf(t, "admin2 tw relay status does not show the provisioned relay:\n%s", statusOut)
	}

	// (e) Administer the relay from admin2.
	regOut := execIn(t, "admin2", "tw relay get-servers")
	if !strings.Contains(regOut, "server1") {
		fatalf(t, "admin2 get-servers does not list server1:\n%s", regOut)
	}
	sid := strings.Trim(strings.TrimSpace(strings.TrimPrefix(adminSID, "server_id:")), `"'`)
	route := "/tw/" + sid
	if n := strings.Count(execIn(t, "relay", "cat /etc/caddy/Caddyfile"), route); n != 1 {
		fatalf(t, "relay Caddyfile has %d occurrences of %s before add-server, want 1", n, route)
	}

	// Same detached dance as SelfEnroll: add-server re-renders the Caddyfile,
	// wiping the local_certs shim before its SSH-dial retry budget runs out.
	execDetached(t, "admin2", "tw relay add-server migsrv > /shared/migrate-addserver.log 2>&1")
	defer execIn(t, "admin", "rm -f /shared/migrate-addserver.log")
	waitFor(t, "admin2 add-server step 3 (Apply relay config) complete", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin2", "cat /shared/migrate-addserver.log 2>/dev/null")
		return strings.Contains(out, "Caddyfile reloaded"), out
	})
	localCertsShim(t)
	waitFor(t, "admin2 add-server completion", 60*time.Second, func() (bool, string) {
		out, _ := execInOK("admin2", "cat /shared/migrate-addserver.log 2>/dev/null")
		return strings.Contains(out, "created and enrolled"), out
	})
	addLog := execIn(t, "admin2", "cat /shared/migrate-addserver.log")
	m := regexp.MustCompile(`server-id (\S+), port (\d+)\)`).FindStringSubmatch(addLog)
	if m == nil {
		fatalf(t, "admin2 add-server output missing server-id/port:\n%s", addLog)
	}
	migID := m[1]
	if cur := strings.TrimSpace(execIn(t, "admin2", "tw config current-context")); cur != ctxName {
		fatalf(t, "add-server on admin2 switched the current context to %q (want %q)", cur, ctxName)
	}
	out = execIn(t, "admin2", "tw relay un-enroll-server "+migID+" --yes")
	if !strings.Contains(out, "Un-enrolled "+migID) {
		fatalf(t, "admin2 un-enroll of %s did not report success:\n%s", migID, out)
	}
	localCertsShim(t)
	execIn(t, "admin2", "tw config delete-context migsrv")

	if h := identityHashes(t, "admin2"); h != adminHashes {
		fatalf(t, "admin2 identity changed across add-server/un-enroll:\nbefore:\n%s\nafter:\n%s", adminHashes, h)
	}
	if p := configLine(t, "admin2", "path"); p != adminPath {
		fatalf(t, "admin2 config path line changed: %q -> %q", adminPath, p)
	}
	caddy := execIn(t, "relay", "cat /etc/caddy/Caddyfile")
	if n := strings.Count(caddy, route); n != 1 {
		fatalf(t, "relay Caddyfile has %d occurrences of %s after admin2's add-server/un-enroll, want 1:\n%s", n, route, caddy)
	}
	if strings.Contains(caddy, "/tw/"+h2+"-") {
		fatalf(t, "relay Caddyfile gained a route derived from admin2's hostname %q:\n%s", h2, caddy)
	}
	out = execIn(t, "admin2", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin2 tw relay test failed:\n%s", out)
	}

	// (f) Regression guard: server1 still carries user traffic, and the
	// original admin still sees it. Alice is gone since Revocation, so a
	// throwaway user in its own --config-dir leaves the client's main profile
	// untouched.
	regOut = execIn(t, "admin", "tw relay get-servers")
	if !strings.Contains(regOut, "server1") {
		fatalf(t, "original admin get-servers no longer lists server1:\n%s", regOut)
	}
	out = execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "original admin tw relay test failed after migration:\n%s", out)
	}
	runInviteExchange(t, "server", "tw server user invite miguser -m "+migPort+":"+echoPort,
		"client", "tw --config-dir /tmp/twmig join "+domain+" {code}")
	defer func() {
		killMatching(t, "client", "twmig")
		execIn(t, "client", "rm -rf /tmp/twmig")
		execIn(t, "server", "printf 'y\\n' | tw server user unregister miguser")
		execIn(t, "server", "printf 'y\\n' | tw server user delete miguser")
	}()
	execDetached(t, "client", "tw --config-dir /tmp/twmig client connect > /var/log/tw-client-mig.log 2>&1")
	waitFor(t, "miguser tunnel listening", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+migPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client-mig.log")
			return false, tail
		}
		return true, ""
	})
	echoOut := execIn(t, "client", "printf 'hello-tw-migration' | nc -w 10 127.0.0.1 "+migPort)
	if strings.TrimSpace(echoOut) != "hello-tw-migration" {
		fatalf(t, "echo round-trip to server1 after migration mismatch: %q", echoOut)
	}
}

// identityHashes returns the sha256 lines of the profile's CA and client cert.
func identityHashes(t *testing.T, service string) string {
	t.Helper()
	return strings.TrimSpace(execIn(t, service,
		"cd /etc/tw-test && sha256sum ca.crt ca.key client.crt"))
}

// configLine returns the trimmed `key:` line(s) of the live config.yaml.
func configLine(t *testing.T, service, key string) string {
	t.Helper()
	out := strings.TrimSpace(execIn(t, service, "grep -E '^[[:space:]]*"+key+":' /etc/tw-test/config.yaml"))
	if out == "" {
		fatalf(t, "%s config.yaml has no %s: line", service, key)
	}
	return out
}
