package ops

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestUploadClientConfigWritesSecrets0600 is the re-audit regression: the
// client-config import path (a sibling of config.Save / the profile-bundle
// unpack) must not write config.yaml — which carries the VLESS UUID and proxy
// credentials — or private keys world-readable (finding #12).
func TestUploadClientConfigWritesSecrets0600(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"config.yaml":    "mode: client\n",
		"id_ed25519":     "PRIVATE",
		"client.key":     "PRIVATE",
		"id_ed25519.pub": "ssh-ed25519 AAAA",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := o.UploadClientConfig(buf.Bytes()); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"config.yaml", "id_ed25519", "client.key"} {
		fi, err := os.Stat(filepath.Join(config.Dir(), name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perms = %o, want 600", name, perm)
		}
	}
}
