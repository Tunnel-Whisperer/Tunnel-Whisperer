package ops

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	relaytenant "github.com/tunnelwhisperer/tw/internal/relay/tenant"
	gossh "golang.org/x/crypto/ssh"
)

// fakeRelaySSH is an in-process sshd standing in for the relay: it accepts
// key auth only for allowUser and answers every exec request with exec(cmd,
// stdin) -> (stdout, exit status). It records the users that tried to log in
// and the commands run.
type fakeRelaySSH struct {
	addr      string
	allowUser string
	exec      func(cmd, stdin string) (string, uint32)

	mu       sync.Mutex
	attempts []string
	cmds     []string
}

func startFakeRelaySSH(t *testing.T, allowUser string, exec func(cmd, stdin string) (string, uint32)) *fakeRelaySSH {
	t.Helper()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRelaySSH{allowUser: allowUser, exec: exec}
	sc := &gossh.ServerConfig{
		PublicKeyCallback: func(c gossh.ConnMetadata, _ gossh.PublicKey) (*gossh.Permissions, error) {
			f.mu.Lock()
			f.attempts = append(f.attempts, c.User())
			f.mu.Unlock()
			if c.User() != f.allowUser {
				return nil, io.EOF
			}
			return nil, nil
		},
	}
	sc.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f.addr = ln.Addr().String()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(nc, sc)
		}
	}()
	return f
}

func (f *fakeRelaySSH) serve(nc net.Conn, sc *gossh.ServerConfig) {
	_, chans, reqs, err := gossh.NewServerConn(nc, sc)
	if err != nil {
		nc.Close()
		return
	}
	go gossh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(gossh.UnknownChannelType, "only sessions")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range creqs {
				if req.Type != "exec" || len(req.Payload) < 4 {
					req.Reply(false, nil)
					continue
				}
				cmd := string(req.Payload[4:])
				req.Reply(true, nil)
				f.mu.Lock()
				f.cmds = append(f.cmds, cmd)
				f.mu.Unlock()
				in, _ := io.ReadAll(ch)
				out, status := f.exec(cmd, string(in))
				ch.Write([]byte(out))
				st := make([]byte, 4)
				binary.BigEndian.PutUint32(st, status)
				ch.SendRequest("exit-status", false, st)
				return
			}
		}()
	}
}

func (f *fakeRelaySSH) dial(t *testing.T, user string) *gossh.Client {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := gossh.NewSignerFromKey(priv)
	c, err := gossh.Dial("tcp", f.addr, &gossh.ClientConfig{
		User: user, Auth: []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestRelayTenantRequest(t *testing.T) {
	const u = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	relay := startFakeRelaySSH(t, "twt-000000000000", func(cmd, stdin string) (string, uint32) {
		if cmd != relaytenant.RequestPath {
			return "", 127
		}
		switch stdin {
		case "list\n":
			return "ok " + u + " other\n", 0
		case "add " + u + "\n":
			return "ok added, applied via xray api\n", 0
		default:
			return "err unknown request\n", 1
		}
	})
	client := relay.dial(t, "twt-000000000000")

	ids, err := relayTenantList(client)
	if err != nil || len(ids) != 2 || ids[0] != u {
		t.Fatalf("relayTenantList = %v, %v", ids, err)
	}
	if reply, err := relayTenantRequest(client, "add "+u); err != nil || reply != "added, applied via xray api" {
		t.Fatalf("add = %q, %v", reply, err)
	}
	_, err = relayTenantRequest(client, "remove "+u)
	if err == nil || !strings.Contains(err.Error(), `relay refused "remove" request: unknown request`) {
		t.Fatalf("err reply must surface as an error, got %v", err)
	}
	// The request travels on stdin; the only command ever sent is the
	// forced-command path itself (never sudo, cat or tee).
	for _, c := range relay.cmds {
		if c != relaytenant.RequestPath {
			t.Errorf("tenant ran %q on the relay", c)
		}
	}
}

// TestRelayTenantRequestLegacyRelay: on a relay that predates tenant
// accounts the request program does not exist; the error must say what to do.
func TestRelayTenantRequestLegacyRelay(t *testing.T) {
	relay := startFakeRelaySSH(t, "ubuntu", func(cmd, stdin string) (string, uint32) {
		return "", 127
	})
	_, err := relayTenantRequest(relay.dial(t, "ubuntu"), "list")
	if err == nil || !strings.Contains(err.Error(), "tw relay sync-tenants") {
		t.Fatalf("legacy relay error = %v, want a pointer to 'tw relay sync-tenants'", err)
	}
}

// TestDialRelaySSHMigratesTenantUser: a server enrolled before per-tenant
// accounts has the admin's account stored; once the relay only accepts its
// key on its own account it switches once and saves that.
func TestDialRelaySSHMigratesTenantUser(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Mode = "server"
	cfg.Xray.ServerID = "srv-1"
	cfg.Server.RelaySSHUser = "ubuntu"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	tenantUser := relaytenant.UserName("srv-1")
	relay := startFakeRelaySSH(t, tenantUser, func(string, string) (string, uint32) { return "", 0 })
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := gossh.NewSignerFromKey(priv)

	client, err := dialRelaySSH(relay.addr, cfg, signer, gossh.InsecureIgnoreHostKey(), 5*time.Second)
	if err != nil {
		t.Fatalf("dialRelaySSH: %v", err)
	}
	client.Close()
	if got := strings.Join(relay.attempts, ","); got != "ubuntu,"+tenantUser {
		t.Errorf("login attempts = %s, want the stored user then the tenant account", got)
	}
	if cfg.Server.RelaySSHUser != tenantUser {
		t.Errorf("in-memory relay user = %q, want %q", cfg.Server.RelaySSHUser, tenantUser)
	}
	saved, err := config.Load()
	if err != nil || saved.Server.RelaySSHUser != tenantUser {
		t.Fatalf("saved relay user = %q (%v), want %q", saved.Server.RelaySSHUser, err, tenantUser)
	}

	// Already migrated: one attempt, no fallback.
	relay.attempts = nil
	client, err = dialRelaySSH(relay.addr, cfg, signer, gossh.InsecureIgnoreHostKey(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if len(relay.attempts) != 1 {
		t.Errorf("attempts after migration = %v", relay.attempts)
	}
}

// TestDialRelaySSHAdminNoFallback: the relay admin's own profile keeps its
// configured account and never tries a tenant account.
func TestDialRelaySSHAdminNoFallback(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Mode = "relay"
	cfg.Xray.ServerID = "adm-1"
	cfg.Server.RelaySSHUser = "ubuntu"
	relay := startFakeRelaySSH(t, relaytenant.UserName("adm-1"), func(string, string) (string, uint32) { return "", 0 })
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := gossh.NewSignerFromKey(priv)
	if _, err := dialRelaySSH(relay.addr, cfg, signer, gossh.InsecureIgnoreHostKey(), 5*time.Second); err == nil {
		t.Fatal("admin profile must not fall back to a tenant account")
	}
	if len(relay.attempts) != 1 || relay.attempts[0] != "ubuntu" {
		t.Errorf("admin attempts = %v", relay.attempts)
	}
}

// TestRelayTenantApplyNotLive (M1): a change the relay saved but could not
// apply to its running Xray is reported to the operator as an error naming
// the affected users; the other requests still go through.
func TestRelayTenantApplyNotLive(t *testing.T) {
	const u1, u2 = "6ba7b810-9dad-11d1-80b4-00c04fd430c8", "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	relay := startFakeRelaySSH(t, "twt-tenant", func(cmd, stdin string) (string, uint32) {
		if stdin == "add "+u1+"\n" {
			return "ok added, saved; " + relaytenant.LiveApplyFailed + " (xray api unreachable), takes effect on next xray restart\n", 0
		}
		return "ok added, applied via xray api\n", 0
	})
	client := relay.dial(t, "twt-tenant")
	err := relayTenantApplyOn(client, "add", []string{u1, u2})
	if err == nil || !strings.Contains(err.Error(), "6ba7b810") || strings.Contains(err.Error(), "6ba7b811") ||
		!strings.Contains(err.Error(), "takes effect when the relay's Xray restarts") {
		t.Fatalf("not-live add must surface as an error naming its user, got %v", err)
	}
	if len(relay.cmds) != 2 {
		t.Errorf("requests sent = %d, want both", len(relay.cmds))
	}
	if err := relayTenantApplyOn(client, "add", []string{u2}); err != nil {
		t.Errorf("live add: %v", err)
	}
}

// TestDialRelaySSHOtherContextUntouched: when the active profile on disk is
// no longer the one that dialed (a context switch since), the adopted tenant
// account is not written into it.
func TestDialRelaySSHOtherContextUntouched(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Mode = "server"
	cfg.Xray.ServerID = "srv-1"
	cfg.Xray.UUID = "11111111-0000-4000-8000-000000000001"
	cfg.Server.RelaySSHUser = "ubuntu"
	other := *cfg
	other.Xray.ServerID = "srv-other"
	other.Xray.UUID = "22222222-0000-4000-8000-000000000002"
	if err := config.Save(&other); err != nil {
		t.Fatal(err)
	}
	tenantUser := relaytenant.UserName("srv-1")
	relay := startFakeRelaySSH(t, tenantUser, func(string, string) (string, uint32) { return "", 0 })
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := gossh.NewSignerFromKey(priv)
	client, err := dialRelaySSH(relay.addr, cfg, signer, gossh.InsecureIgnoreHostKey(), 5*time.Second)
	if err != nil {
		t.Fatalf("dialRelaySSH: %v", err)
	}
	client.Close()
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Xray.ServerID != "srv-other" || saved.Server.RelaySSHUser != "ubuntu" {
		t.Errorf("the other context's profile was changed: server %q, relay user %q", saved.Xray.ServerID, saved.Server.RelaySSHUser)
	}
}

// TestDialRelaySSHUpdatesLiveOps: a dial with a copy of the daemon's profile
// (o.Config()) also moves the daemon's in-memory profile to the tenant
// account, so its next save does not write the old user back.
func TestDialRelaySSHUpdatesLiveOps(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Mode = "server"
	cfg.Xray.ServerID = "srv-1"
	cfg.Server.RelaySSHUser = "ubuntu"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	o := &Ops{cfg: cfg}
	trackLiveOps(o)
	t.Cleanup(func() { liveOps.Delete(o) })
	tenantUser := relaytenant.UserName("srv-1")
	relay := startFakeRelaySSH(t, tenantUser, func(string, string) (string, uint32) { return "", 0 })
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := gossh.NewSignerFromKey(priv)
	client, err := dialRelaySSH(relay.addr, o.Config(), signer, gossh.InsecureIgnoreHostKey(), 5*time.Second)
	if err != nil {
		t.Fatalf("dialRelaySSH: %v", err)
	}
	client.Close()
	deadline := time.Now().Add(5 * time.Second)
	for o.Config().Server.RelaySSHUser != tenantUser {
		if time.Now().After(deadline) {
			t.Fatalf("daemon's in-memory relay user = %q, want %q", o.Config().Server.RelaySSHUser, tenantUser)
		}
		time.Sleep(10 * time.Millisecond)
	}
	o.mu.Lock()
	err = config.Save(o.cfg)
	o.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := config.Load(); err != nil || saved.Server.RelaySSHUser != tenantUser {
		t.Errorf("saved relay user after the daemon saved its profile = %q (%v), want %q", saved.Server.RelaySSHUser, err, tenantUser)
	}
}
