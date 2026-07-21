package ops

import (
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestGenerateManualInstallScriptStampsAdminMode is a regression test for the
// bug where no code path ever set mode "admin": a relay created from a fresh
// profile left mode empty, so the context listing showed a blank ROLE and the
// relay handle was rendered with the fallback "server" role.
func TestGenerateManualInstallScriptStampsAdminMode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())

	// Fresh profile: no config.yaml at all, the state `tw relay create` runs in.
	o, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	if _, err := o.GenerateManualInstallScript("relay.example.com", false); err != nil {
		t.Fatalf("GenerateManualInstallScript: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if cfg.Mode != "admin" {
		t.Errorf("persisted mode = %q, want admin", cfg.Mode)
	}

	// An existing non-empty mode must never be overwritten.
	if err := os.WriteFile(config.FilePath(), []byte("mode: server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o2, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	if _, err := o2.GenerateManualInstallScript("relay.example.com", false); err != nil {
		t.Fatalf("GenerateManualInstallScript (server mode): %v", err)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if cfg.Mode != "server" {
		t.Errorf("mode was overwritten to %q, want server kept", cfg.Mode)
	}
}
