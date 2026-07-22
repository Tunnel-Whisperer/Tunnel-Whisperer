package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

func writeProfileKey(t *testing.T) {
	t.Helper()
	priv, pub, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "id_ed25519"), priv, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "id_ed25519.pub"), pub, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStampModeAuthSignsCurrentMode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t)
	o, _ := New()
	cfg := o.Config()
	cfg.Mode = "relay"
	if err := o.stampModeAuth(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ModeAuth == nil || cfg.ModeAuth.Sig == "" {
		t.Fatal("stampModeAuth did not set a signature")
	}
	id, _ := profileIdentity()
	if err := modeauth.Verify("relay", id, cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err != nil {
		t.Errorf("stamped signature does not verify: %v", err)
	}
	// A different mode must NOT verify against the stamped signature.
	if err := modeauth.Verify("server", id, cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err == nil {
		t.Error("signature verified for the wrong mode")
	}
}
