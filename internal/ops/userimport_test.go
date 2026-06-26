package ops

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func makeUserBundle(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"config.yaml":    "xray:\n  relay_host: relay.example.com\n",
		"client.crt":     "CERT",
		"client.key":     "KEY",
		"id_ed25519":     "SSHKEY",
		"id_ed25519.pub": "ssh-ed25519 AAAA",
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportUserBundle(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := ImportUserBundle(makeUserBundle(t)); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{config.FilePath(), config.ClientCertPath(), config.ClientKeyPath(),
		filepath.Join(config.Dir(), "id_ed25519")} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing after import: %s (%v)", f, err)
		}
	}
	// Mode is set to client.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "client" {
		t.Errorf("mode = %q, want client", cfg.Mode)
	}
	if cfg.Xray.RelayHost != "relay.example.com" {
		t.Errorf("relay host not imported: %q", cfg.Xray.RelayHost)
	}
}

func TestImportUserBundleRejectsZipSlip(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../escape.txt")
	w.Write([]byte("x"))
	zw.Close()
	if err := ImportUserBundle(buf.Bytes()); err == nil {
		t.Error("expected zip-slip rejection")
	}
}
