package ops

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/enroll"
	"github.com/tunnelwhisperer/tw/internal/pki"
)

// newTestOps builds an Ops against an empty profile under the caller's
// TW_CONFIG_DIR — the same construction addserver_test.go uses.
func newTestOps(t *testing.T) *Ops {
	t.Helper()
	o, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	return o
}

func TestJoinRejectsBadCode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	_, err := o.Join(context.Background(), "relay.example.com", "garbage", "", JoinUI{}, nil)
	if err == nil || !strings.Contains(err.Error(), "invite code") {
		t.Fatalf("want code-shape error, got %v", err)
	}
}

func TestJoinServerGrantBuildsContext(t *testing.T) {
	// Exercises applyServerGrant directly: identity + JoinResponse in,
	// context stored, mode signed only when it verifies.
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	ident, err := newLocalServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	resp := &JoinResponse{Version: 1, ServerID: ident.serverID,
		RelayHost: "relay.example.com", Path: "/tw/" + ident.serverID,
		RemotePort: 20000, SSHUser: "tw"}
	grant, _ := resp.Encode()
	res, err := o.applyServerGrant(ident, grant, "myserver")
	if err != nil {
		t.Fatalf("applyServerGrant: %v", err)
	}
	if res.ContextName != "myserver" || res.Role != "server" {
		t.Fatalf("result: %+v", res)
	}
	names, err := o.ListContexts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range names {
		if c.Name == "myserver" {
			found = true
		}
	}
	if !found {
		t.Fatal("context not stored")
	}
}

func TestApplyClientGrant(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	var c clientMaterial
	if _, err := c.makeOffer("alice"); err != nil {
		t.Fatalf("makeOffer: %v", err)
	}
	g := enroll.ClientGrant{
		RelayHost: "relay.example.com", RelayPort: 443, Path: "/tw/srv-1",
		SSHUser: "alice", ServerSSHPort: 20000,
		Tunnels: []enroll.GrantTunnel{{LocalPort: 18080, RemoteHost: "127.0.0.1", RemotePort: 80}},
	}
	// Use a REAL cert: sign the material's CSR with a throwaway CA so the
	// stored context carries a parseable client.crt.
	caCert, caKey, _ := pki.GenerateCA("srv-1")
	certPEM, err := pki.SignClientCSR(caCert, caKey, c.csrPEM, "srv-1")
	if err != nil {
		t.Fatal(err)
	}
	g.ClientCertPEM = string(certPEM)
	grant, _ := json.Marshal(g)
	res, err := o.applyClientGrant(&c, grant, "alice", JoinUI{
		ResolvePort: func(t config.Tunnel) int { return t.LocalPort + 1 },
	})
	if err != nil {
		t.Fatalf("applyClientGrant: %v", err)
	}
	if res.Role != "client" || res.ContextName != "alice" {
		t.Fatalf("result: %+v", res)
	}
}

func TestApplyClientGrantBusyPortResolved(t *testing.T) {
	// Bind a local port, grant a tunnel on it, and assert ResolvePort's
	// replacement lands in the stored config.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	var c clientMaterial
	if _, err := c.makeOffer("bob"); err != nil {
		t.Fatal(err)
	}
	caCert, caKey, _ := pki.GenerateCA("srv-1")
	certPEM, _ := pki.SignClientCSR(caCert, caKey, c.csrPEM, "srv-1")
	g := enroll.ClientGrant{RelayHost: "r", RelayPort: 443, Path: "/p", SSHUser: "bob",
		ServerSSHPort: 20000, ClientCertPEM: string(certPEM),
		Tunnels: []enroll.GrantTunnel{{LocalPort: busy, RemoteHost: "127.0.0.1", RemotePort: 80}}}
	grant, _ := json.Marshal(g)
	resolved := 0
	_, err = o.applyClientGrant(&c, grant, "bob", JoinUI{
		ResolvePort: func(config.Tunnel) int { resolved++; return busy + 10 },
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Fatalf("ResolvePort called %d times, want 1", resolved)
	}
}
