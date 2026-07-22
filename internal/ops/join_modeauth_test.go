package ops

import (
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// TestApplyJoinResponseRejectsForeignModeSignature is a regression test for
// the lockout bug: a JoinResponse carrying a mode signature that does not
// verify against THIS server's own identity must not be persisted into
// cfg.ModeAuth, or every subsequent command would fail closed with "mode
// signature invalid" (bricking the server). The rest of the response
// (relay coords, remote port) must still apply.
func TestApplyJoinResponseRejectsForeignModeSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t) // this server's own key
	o := newOpsForTest(t)

	// Sign over a completely different identity, not this server's pubkey.
	foreignPriv, _, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	sig, issuer, err := modeauth.Sign(foreignPriv, "server", "ssh-ed25519 AAAAsomeoneelse not-this-server")
	if err != nil {
		t.Fatal(err)
	}

	resp := &JoinResponse{
		Version:    1,
		ServerID:   "srv-1",
		RelayHost:  "relay.example.com",
		Path:       "/tw/srv-1",
		RemotePort: 20000,
		SSHUser:    "srv-1",
		ModeSig:    sig,
		ModeIssuer: issuer,
	}
	if err := o.ApplyJoinResponse(resp); err != nil {
		t.Fatalf("ApplyJoinResponse returned error: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModeAuth != nil {
		t.Fatalf("cfg.ModeAuth persisted for a foreign signature; server would be bricked: %+v", cfg.ModeAuth)
	}
	// The rest of the response must still have applied.
	if cfg.Xray.RelayHost != "relay.example.com" || cfg.Xray.Path != "/tw/srv-1" {
		t.Errorf("relay coords not persisted: relay_host=%q path=%q", cfg.Xray.RelayHost, cfg.Xray.Path)
	}
	if cfg.Server.RemotePort != 20000 {
		t.Errorf("remote_port not persisted: %d", cfg.Server.RemotePort)
	}
}

// TestApplyJoinResponsePersistsValidModeSignature is the positive
// counterpart: a response correctly relay-signed over this server's own
// identity (the same shape EnrollServer/signServerMode produces) must be
// persisted into cfg.ModeAuth.
func TestApplyJoinResponsePersistsValidModeSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t) // this server's own key
	o := newOpsForTest(t)

	id, err := profileIdentity()
	if err != nil {
		t.Fatal(err)
	}
	relayPriv, _, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	sig, issuer, err := modeauth.Sign(relayPriv, "server", id)
	if err != nil {
		t.Fatal(err)
	}

	resp := &JoinResponse{
		Version:    1,
		ServerID:   "srv-1",
		RelayHost:  "relay.example.com",
		Path:       "/tw/srv-1",
		RemotePort: 20000,
		SSHUser:    "srv-1",
		ModeSig:    sig,
		ModeIssuer: issuer,
	}
	if err := o.ApplyJoinResponse(resp); err != nil {
		t.Fatalf("ApplyJoinResponse returned error: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModeAuth == nil || cfg.ModeAuth.Sig == "" {
		t.Fatal("valid mode signature was not persisted")
	}
	if err := modeauth.Verify("server", strings.TrimSpace(id), cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err != nil {
		t.Errorf("persisted signature does not verify: %v", err)
	}
}
