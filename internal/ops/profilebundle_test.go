package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSealUnsealProfileRoundTrip(t *testing.T) {
	src := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", src)
	writeFile(t, config.FilePath(), "mode: admin\n")
	writeFile(t, config.CACertPath(), "CA")
	writeFile(t, filepath.Join(config.RelayDir(), "manual-relay.json"), `{"domain":"a"}`)
	writeFile(t, filepath.Join(config.Dir(), "users", "alice", "config.yaml"), "user: alice")

	sealed, err := sealProfile("pw")
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dst)
	if err := unsealProfile(sealed, "pw"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{config.FilePath(), config.CACertPath(),
		filepath.Join(config.RelayDir(), "manual-relay.json"),
		filepath.Join(config.Dir(), "users", "alice", "config.yaml")} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing after unseal: %s (%v)", f, err)
		}
	}
}

func TestUnsealProfileWrongPassphrase(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeFile(t, config.FilePath(), "mode: admin\n")
	sealed, err := sealProfile("pw")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := unsealProfile(sealed, "wrong"); err == nil {
		t.Error("expected error with wrong passphrase")
	}
}
