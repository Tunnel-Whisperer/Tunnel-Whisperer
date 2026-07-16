package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// Re-importing the active context must refresh the live profile, not keep the
// stale one. Regression for: edit a user's mapping on the server, re-export,
// re-import on the client -> connect still used the old mapping.
func TestReapplyContextRefreshesLiveProfile(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)

	// Build a sealed client context whose config carries the NEW relay_port.
	writeFile(t, config.ClientCertPath(), "CERT")
	writeFile(t, config.ClientKeyPath(), "KEY")
	ud := filepath.Join(config.UsersDir(), "alice")
	writeFile(t, filepath.Join(ud, "config.yaml"), "xray:\n  relay_host: relay.example.com\n  relay_port: 8443\n")
	writeFile(t, filepath.Join(ud, "id_ed25519"), "K")
	writeFile(t, filepath.Join(ud, "id_ed25519.pub"), "ssh-ed25519 AAAA")
	bundle, err := o.GetUserConfigBundle("alice")
	if err != nil {
		t.Fatal(err)
	}

	// Store it as the active context, with stale live content still on disk.
	if err := os.MkdirAll(config.ContextsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ContextBundlePath("ctx"), bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, _ := config.LoadContextIndex()
	idx.Contexts["ctx"] = config.ContextMeta{Role: "client", Relay: "relay.example.com"}
	idx.CurrentContext = "ctx"
	if err := config.SaveContextIndex(idx); err != nil {
		t.Fatal(err)
	}

	if err := o.ReapplyContext("ctx", nil); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Xray.RelayPort != 8443 {
		t.Errorf("RelayPort = %d after reapply, want 8443 (live profile not refreshed)", cfg.Xray.RelayPort)
	}
	if cfg.Mode != "client" {
		t.Errorf("Mode = %q, want client", cfg.Mode)
	}
}
