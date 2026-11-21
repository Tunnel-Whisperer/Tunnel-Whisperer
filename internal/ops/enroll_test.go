package ops

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	relaytenant "github.com/tunnelwhisperer/tw/internal/relay/tenant"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// genPubKey returns a real, canonical ("<type> <base64>", no comment)
// authorized_keys public key for use as a test fixture.
func genPubKey(t *testing.T) string {
	t.Helper()
	_, pub, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(pub))
}

// TestRenderRelayAuthorizedKeys: the admin account's authorized_keys holds
// the admin key only, newline-terminated (C1: tenant keys used to share this
// NOPASSWD-sudo account and could exec as it).
func TestRenderRelayAuthorizedKeys(t *testing.T) {
	admin := genPubKey(t)
	out := renderRelayAuthorizedKeys(admin+"\n", false)
	if out != `from="127.0.0.1" `+admin+"\n" {
		t.Errorf("admin authorized_keys = %q, want only the pinned admin line", out)
	}
}

// TestRenderTenantAuthorizedKeys is the C1 regression: a tenant's line
// carries command= forcing tw-tenant-request, so OpenSSH runs nothing else
// for any exec/shell/subsystem request, while -R to the tenant's own ports
// still works. It also keeps the second-tenant-bug invariant: exactly one
// newline-terminated line.
func TestRenderTenantAuthorizedKeys(t *testing.T) {
	key := genPubKey(t)
	line, err := renderTenantAuthorizedKeys(RegisteredServer{ServerID: "srv-1", RemotePort: 20000, SSHPubkey: key + "\n"})
	if err != nil {
		t.Fatal(err)
	}
	want := `from="127.0.0.1",restrict,command="/usr/local/lib/tw/tw-tenant-request",port-forwarding,` +
		`permitopen="127.0.0.1:1",permitlisten="127.0.0.1:20000",permitlisten="127.0.0.1:40000" ` + key + "\n"
	if line != want {
		t.Errorf("tenant line =\n%q\nwant\n%q", line, want)
	}
}

// TestRenderTenantAuthorizedKeysRejectsInjection is the SP-1/SP-3
// regression: a tenant key carrying an embedded newline (plus an
// unrestricted second line) must not produce an authorized_keys line, and
// the tenant gets no relay account at all (fail closed).
func TestRenderTenantAuthorizedKeysRejectsInjection(t *testing.T) {
	evil := genPubKey(t)
	poisoned := genPubKey(t) + "\n" + evil
	srv := RegisteredServer{ServerID: "srv-1", RemotePort: 20000, SSHPubkey: poisoned}
	if line, err := renderTenantAuthorizedKeys(srv); err == nil {
		t.Fatalf("poisoned key must be rejected, got %q", line)
	}
	script, err := renderTenantSync("ubuntu", "ssh-ed25519 AAAAadmin\n", []RegisteredServer{srv})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "tw_tenant_ensure "+relaytenant.UserName("srv-1")) {
		t.Fatalf("poisoned tenant must get no relay account:\n%s", script)
	}
}

// TestRenderTenantSyncAccounts: every registered server gets its own account
// whose authorized_keys is exactly its forced-command line, plus its tunnel
// and enroll ports (for ending pre-migration sessions on them).
func TestRenderTenantSyncAccounts(t *testing.T) {
	k1, k2 := genPubKey(t), genPubKey(t)
	servers := []RegisteredServer{
		{ServerID: "srv-1", RemotePort: 20000, SSHPubkey: k1},
		{ServerID: "srv-2", RemotePort: 20001, SSHPubkey: k2},
	}
	adminAK := renderRelayAuthorizedKeys(genPubKey(t), false)
	script, err := renderTenantSync("ubuntu", adminAK, servers)
	if err != nil {
		t.Fatal(err)
	}
	// L3: the admin's keys are rewritten by the same script, after every
	// tenant account exists.
	adminCall := "tw_admin_keys ubuntu " + base64.StdEncoding.EncodeToString([]byte(adminAK)) + "\n"
	if at := strings.Index(script, adminCall); at < 0 || at < strings.LastIndex(script, "tw_tenant_ensure ") {
		t.Errorf("sync script must rewrite the admin keys after the tenant accounts:\n%s", script)
	}
	for _, s := range servers {
		ak, err := renderTenantAuthorizedKeys(s)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("tw_tenant_ensure %s %s %s %s %d %d\n", relaytenant.UserName(s.ServerID), s.ServerID,
			base64.StdEncoding.EncodeToString([]byte(ak)),
			base64.StdEncoding.EncodeToString([]byte(s.ServerID+"\n")),
			s.RemotePort, enrollPort(s.RemotePort))
		if !strings.Contains(script, want) {
			t.Errorf("sync script lacks %q", want)
		}
	}
}

// TestRenderRelayAuthorizedKeysSSHOpen: --ssh-open means the admin key must
// actually WORK over the open port 22 — so the admin line drops its
// from="127.0.0.1" pin (which rejects any non-loopback source and made the
// open port unusable with tw's own key). Tenant lines live in the tenants'
// own accounts and stay pinned regardless.
func TestRenderRelayAuthorizedKeysSSHOpen(t *testing.T) {
	admin := genPubKey(t)
	if out := renderRelayAuthorizedKeys(admin, true); out != admin+"\n" {
		t.Errorf("ssh-open admin authorized_keys = %q, want the unpinned admin line only", out)
	}
	line, err := renderTenantAuthorizedKeys(RegisteredServer{RemotePort: 20000, SSHPubkey: genPubKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, `from="127.0.0.1",restrict,command=`) {
		t.Errorf("tenant line must stay pinned, restricted and forced: %q", line)
	}
}

// TestRelayTenantStateSeedsAdminFirst pins the invariant every relay render
// depends on: the admin's own tenant entry is ALWAYS present and ALWAYS
// first — even with zero registered servers — so an enroll/un-enroll can
// never render a Caddyfile/authorized_keys/xray config that locks the
// admin out of its own relay.
func TestRelayTenantStateSeedsAdminFirst(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Xray.UUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"
	o.cfg.Server.RemotePort = 2222
	if err := os.WriteFile(config.CACertPath(), []byte(testCAPEM(t, "admin")), 0o600); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	adminID := deriveServerID(host, o.cfg.Xray.UUID)

	// Zero registered servers: the admin entry alone.
	servers, tenants, caCerts, err := o.relayTenantState(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || len(tenants) != 1 {
		t.Fatalf("empty registry must still yield the admin entry, got %d servers / %d tenants", len(servers), len(tenants))
	}
	if servers[0].ID != adminID || servers[0].Role != "relay" {
		t.Errorf("admin caddy entry = %+v, want ID %s role relay", servers[0], adminID)
	}
	if tenants[0].ServerID != adminID || tenants[0].RemotePort != 2222 {
		t.Errorf("admin tenant = %+v, want ID %s port 2222", tenants[0], adminID)
	}
	if _, ok := caCerts[adminID]; !ok {
		t.Errorf("admin CA cert missing from caCerts (keys: %v)", caCerts)
	}

	// With a registered server: admin still first, tenant appended after.
	servers, tenants, caCerts, err = o.relayTenantState([]RegisteredServer{
		{ServerID: "srv-1", UUID: "u-1", RemotePort: 20000, CACertPEM: "PEM-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].ID != adminID || servers[1].ID != "srv-1" {
		t.Errorf("admin must stay first: %+v", servers)
	}
	if servers[1].Role != "server" {
		t.Errorf("tenant role = %q, want server", servers[1].Role)
	}
	if tenants[1].ServerID != "srv-1" || string(caCerts["srv-1"]) != "PEM-1" {
		t.Errorf("tenant entry not appended correctly: %+v / %q", tenants[1], caCerts["srv-1"])
	}
}

func TestRenderTenantAuthorizedKeysEnrollListen(t *testing.T) {
	out, err := renderTenantAuthorizedKeys(RegisteredServer{ServerID: "s1", RemotePort: 20000, SSHPubkey: genPubKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `permitlisten="127.0.0.1:20000"`) ||
		!strings.Contains(out, `permitlisten="127.0.0.1:40000"`) {
		t.Fatalf("tenant line must permit both the tunnel and enroll listens:\n%s", out)
	}
}

func TestEnrollPort(t *testing.T) {
	if got := enrollPort(20000); got != 40000 {
		t.Fatalf("enrollPort(20000) = %d", got)
	}
}

// TestEnrollServerRollsBackRegistryBeforeRelay is the C7 regression: a
// failure before the relay is touched (here the relay Xray render rejecting
// the uuid) must not leave the tenant in the registry, where it would break
// every later enrollment's render.
func TestEnrollServerRollsBackRegistryBeforeRelay(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	o.cfg.Xray.UUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"
	o.cfg.Xray.RelayHost = "relay.example.com"
	o.cfg.Server.RemotePort = 2222
	if err := os.WriteFile(config.CACertPath(), []byte(testCAPEM(t, "admin")), 0o600); err != nil {
		t.Fatal(err)
	}
	// AddServer now refuses a malformed tenant uuid outright, so make the
	// relay Xray render fail on the admin's own (non-canonical) uuid instead.
	adminUUID := o.cfg.Xray.UUID
	o.cfg.Xray.UUID = "not-a-uuid"
	bad := &JoinRequest{Version: 1, ServerID: "evil-1", UUID: "11111111-2222-3333-4444-555555555556",
		CACertPEM: testCAPEM(t, "evil-1"), SSHPubkey: genPubKey(t)}
	if _, err := o.EnrollServer(bad, nil); err == nil || !strings.Contains(err.Error(), "invalid uuid") {
		t.Fatalf("EnrollServer(render failure) = %v, want xray render failure", err)
	}
	if _, err := os.Stat(filepath.Join(RegistryDir(), "evil-1.json")); !os.IsNotExist(err) {
		t.Fatalf("registry file kept after a pre-relay failure: %v", err)
	}
	o.cfg.Xray.UUID = adminUUID
	list, err := o.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("registry kept %d entries after a pre-relay failure: %+v", len(list), list)
	}

	// The next enrollment is not poisoned: it gets past both renders and
	// stops only at the admin key, the first step after them.
	good := &JoinRequest{Version: 1, ServerID: "honest-1", UUID: "11111111-2222-3333-4444-555555555555",
		CACertPEM: testCAPEM(t, "honest-1"), SSHPubkey: genPubKey(t)}
	if _, err := o.EnrollServer(good, nil); err == nil || !strings.Contains(err.Error(), "admin public key") {
		t.Fatalf("EnrollServer(honest) = %v, want admin-public-key failure", err)
	}
	if list, _ := o.ListServers(); len(list) != 0 {
		t.Fatalf("registry kept %d entries after a pre-relay failure: %+v", len(list), list)
	}
}
