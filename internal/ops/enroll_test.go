package ops

import (
	"os"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// TestRenderRelayAuthorizedKeys is a regression test for the second-tenant
// enroll bug: key lines were appended without a trailing newline, so the
// second server's line glued onto the first server's, corrupting both entries
// and making the relay sshd reject both keys. The file is now fully rewritten
// from the tenant list, every line newline-terminated.
func TestRenderRelayAuthorizedKeys(t *testing.T) {
	servers := []RegisteredServer{
		{RemotePort: 20000, SSHPubkey: "ssh-ed25519 AAAAkey1 s1@tw\n"}, // trailing newline must be trimmed
		{RemotePort: 20001, SSHPubkey: "ssh-ed25519 AAAAkey2 s2@tw"},
	}
	out := renderRelayAuthorizedKeys("ssh-ed25519 AAAAadmin admin@tw\n", servers, false)

	if !strings.HasSuffix(out, "\n") {
		t.Error("authorized_keys content must end with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (admin + 2 servers), got %d:\n%s", len(lines), out)
	}
	if lines[0] != `from="127.0.0.1" ssh-ed25519 AAAAadmin admin@tw` {
		t.Errorf("admin line = %q", lines[0])
	}
	for i, port := range []string{"20000", "20001"} {
		l := lines[i+1]
		for _, want := range []string{
			`from="127.0.0.1"`, "restrict", "port-forwarding",
			`permitopen="127.0.0.1:1"`, `permitlisten="127.0.0.1:` + port + `"`,
		} {
			if !strings.Contains(l, want) {
				t.Errorf("server line %d missing %q: %q", i+1, want, l)
			}
		}
		if strings.Contains(l, "\n") {
			t.Errorf("server line %d contains embedded newline: %q", i+1, l)
		}
	}
}

func TestEnrollServerSignsServerMode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t) // relay's own key (helper from modeauth_wire_test.go)
	// Minimal relay config so EnrollServer can render (relay host + a UUID).
	o, _ := New()
	// The join request carries the SERVER's identity pubkey; reuse a fresh key.
	_, serverPub, _ := twssh.GenerateKeyPair()
	req := &JoinRequest{Version: 1, ServerID: "srv-1", UUID: "u-srv",
		Hostname: "srv", RelayHost: "relay.example", CACertPEM: testCAPEM(t),
		SSHPubkey: strings.TrimSpace(string(serverPub))}
	// Sign only — call the signing helper EnrollServer uses, not the full
	// SSH flow. If EnrollServer cannot be unit-run without a relay, assert on
	// the extracted helper signServerMode(req) instead (see Step 3).
	resp, sig, issuer := signServerModeForTest(t, o, req)
	_ = resp
	if err := modeauth.Verify("server", req.SSHPubkey, sig, issuer); err != nil {
		t.Fatalf("relay-signed server token does not verify: %v", err)
	}
}

// TestRenderRelayAuthorizedKeysSSHOpen: --ssh-open means the admin key must
// actually WORK over the open port 22 — so the admin line drops its
// from="127.0.0.1" pin (which rejects any non-loopback source and made the
// open port unusable with tw's own key). Tenant lines stay pinned and
// forward-only regardless.
func TestRenderRelayAuthorizedKeysSSHOpen(t *testing.T) {
	servers := []RegisteredServer{
		{RemotePort: 20000, SSHPubkey: "ssh-ed25519 AAAAkey1 s1@tw"},
	}
	out := renderRelayAuthorizedKeys("ssh-ed25519 AAAAadmin admin@tw", servers, true)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), out)
	}
	if lines[0] != "ssh-ed25519 AAAAadmin admin@tw" {
		t.Errorf("ssh-open admin line must be unpinned, got %q", lines[0])
	}
	if !strings.Contains(lines[1], `from="127.0.0.1"`) || !strings.Contains(lines[1], "restrict") {
		t.Errorf("tenant line must stay pinned+restricted even with ssh-open: %q", lines[1])
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
	if err := os.WriteFile(config.CACertPath(), []byte(testCAPEM(t)), 0o600); err != nil {
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

func signServerModeForTest(t *testing.T, o *Ops, req *JoinRequest) (*JoinResponse, string, string) {
	t.Helper()
	sig, issuer, err := o.signServerMode(req)
	if err != nil {
		t.Fatal(err)
	}
	return &JoinResponse{ServerID: req.ServerID, ModeSig: sig, ModeIssuer: issuer}, sig, issuer
}
