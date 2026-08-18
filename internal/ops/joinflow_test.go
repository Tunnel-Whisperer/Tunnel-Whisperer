package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// TestJoinFailsBeforeAnyNetworkCall reproduces the reported incident: an
// unwritable config dir must be caught before RunEnrollee's first request
// (/start), because /start burns the issuer's one-time invite. A regular
// file as the config dir's PARENT makes MkdirAll fail deterministically,
// without needing to drop privileges.
func TestJoinFailsBeforeAnyNetworkCall(t *testing.T) {
	// Load the profile from a normal, writable dir first (ops.New/config.Load
	// must succeed to even get an *Ops to call Join on) — then swap
	// TW_CONFIG_DIR to a path whose PARENT is a regular file, so
	// config.CheckWritable (which re-reads TW_CONFIG_DIR on every call, like
	// the rest of the config package) fails deterministically once Join
	// actually runs. This stands in for the real incident: the directory is
	// (or becomes) unwritable, discovered only when tw tries to persist.
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)

	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TW_CONFIG_DIR", filepath.Join(blocker, "config"))

	// The code's shape doesn't matter: the preflight check must fire before
	// ParseCode even runs, let alone before any HTTP call.
	_, err := o.Join(context.Background(), "relay.invalid.example", "irrelevant-code", "", JoinUI{}, nil)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "not writable") || !strings.Contains(err.Error(), "TW_CONFIG_DIR") {
		t.Fatalf("want the CheckWritable preflight error, got: %v", err)
	}
}

// TestApplyServerGrantRescuesOnImportFailure exercises the "never lose a
// delivered grant" path: the invite already burned and the tenant is already
// enrolled on the relay by the time applyServerGrant runs, so if the local
// context store can't be written to, the sealed bundle must be rescued to a
// fallback file instead of silently discarded.
func TestApplyServerGrantRescuesOnImportFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	// Pre-create "contexts" as a regular FILE so ImportContext's
	// os.MkdirAll(config.ContextsDir(), ...) fails.
	if err := os.WriteFile(config.ContextsDir(), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Chdir(work)

	o := newTestOps(t)
	ident, err := newLocalServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	resp := &JoinResponse{Version: 1, ServerID: ident.serverID,
		RelayHost: "relay.example.com", Path: "/tw/" + ident.serverID,
		RemotePort: 20000, SSHUser: "tw"}
	grant, _ := resp.Encode()

	_, err = o.applyServerGrant(ident, grant, "rescueme")
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "tw config import") {
		t.Fatalf("error does not name the recovery command: %v", err)
	}
	wantFile := filepath.Join(work, "tw_rescue_rescueme.twctx")
	if !strings.Contains(err.Error(), wantFile) {
		t.Fatalf("error does not name the rescue file %q: %v", wantFile, err)
	}
	if _, statErr := os.Stat(wantFile); statErr != nil {
		t.Fatalf("rescue file was not written: %v", statErr)
	}
}

// TestRescueGrantBothFallbacksFail covers rescueGrant's last-resort branch:
// cwd AND os.TempDir() both fail to accept the write, so the grant really is
// unrecoverable and the error must say so plainly (not silently drop it).
//
// Skipped as root: cwd is blocked here via a permission bit (chmod 0500),
// which root ignores — there's no root-proof way to make a writable
// directory refuse a write, unlike the structural (EISDIR/ENOTDIR) tricks
// used elsewhere in this suite. TMPDIR is blocked structurally (parent-is-a-
// file) so that half is root-proof; only the cwd half needs the skip.
func TestRescueGrantBothFallbacksFail(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a chmod-based write-denial can't be simulated")
	}

	workDir := t.TempDir()
	if err := os.Chmod(workDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(workDir, 0o700) }) // let t.TempDir() clean up after
	t.Chdir(workDir)

	tmpParent := t.TempDir()
	blocker := filepath.Join(tmpParent, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(blocker, "tmp"))

	err := rescueGrant([]byte("sealed-bundle-bytes"), "victim", fmt.Errorf("import failed"))
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "grant is lost") {
		t.Fatalf("want the both-fallbacks-failed message, got: %v", err)
	}
}
