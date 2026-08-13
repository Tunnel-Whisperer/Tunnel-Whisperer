package ops

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

// sealTestClientBundle zips cfgYAML as config.yaml plus a placeholder SSH
// identity and cryptobox-seals it with no passphrase — the same shape
// unsealProfile/ImportContext consume. Standing in for the deleted
// GetUserConfigBundle, which used to build this shape from an on-server user
// dir; the zero-file enrollment flow no longer keeps one.
func sealTestClientBundle(t *testing.T, cfgYAML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := []struct{ name, data string }{
		{"config.yaml", cfgYAML},
		{"id_ed25519", "K"},
		{"id_ed25519.pub", "ssh-ed25519 AAAA"},
	}
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sealed, err := cryptobox.Encrypt(buf.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// Re-importing the active context must refresh the live profile, not keep the
// stale one. Regression for: edit a user's mapping on the server, re-export,
// re-import on the client -> connect still used the old mapping.
func TestReapplyContextRefreshesLiveProfile(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)

	// Build a sealed client context whose config carries the NEW relay_port.
	bundle := sealTestClientBundle(t, "mode: client\nxray:\n  relay_host: relay.example.com\n  relay_port: 8443\n")

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
