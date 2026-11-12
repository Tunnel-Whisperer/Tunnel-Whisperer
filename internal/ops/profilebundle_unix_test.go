//go:build unix

package ops

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/relay/terraform"
)

// TestUseContextRestoresOwnerOnlyModes: switching back into a relay-admin
// context recreates relay/ and users/<name>/ from the bundle. Under a 022
// umask they must come back 0700 with 0600 files (relay/ carries the host
// private key in cloud-init.yaml and the cloud API token in tfvars), never
// world-readable; public material outside relay/ stays 0644.
func TestUseContextRestoresOwnerOnlyModes(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	idx, err := config.EnsureContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	orig := idx.CurrentContext

	if err := terraform.Generate(config.RelayDir(), terraform.Config{
		Domain: "relay.example.com", Provider: "hetzner", HostKeyB64: "RFVNTVktSE9TVC1LRVk=",
	}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"terraform.tfvars", "terraform.tfstate"} {
		if err := os.WriteFile(filepath.Join(config.RelayDir(), f), []byte("DUMMY_TOKEN"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	alice := filepath.Join(config.UsersDir(), "alice")
	if err := os.MkdirAll(alice, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alice, "notes"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alice, "id_ed25519.pub"), []byte("ssh-ed25519 AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := o.NewContext("fresh"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.RelayDir()); !os.IsNotExist(err) {
		t.Fatalf("relay/ should be cleared by NewContext: %v", err)
	}
	if err := o.UseContext(orig, nil); err != nil {
		t.Fatal(err)
	}

	want := map[string]os.FileMode{
		config.RelayDir(): 0o700,
		filepath.Join(config.RelayDir(), "cloud-init.yaml"):   0o600,
		filepath.Join(config.RelayDir(), "main.tf"):           0o600,
		filepath.Join(config.RelayDir(), "terraform.tfvars"):  0o600,
		filepath.Join(config.RelayDir(), "terraform.tfstate"): 0o600,
		config.UsersDir():                      0o700,
		alice:                                  0o700,
		filepath.Join(alice, "notes"):          0o600,
		filepath.Join(alice, "id_ed25519.pub"): 0o644,
		config.FilePath():                      0o600,
	}
	for p, m := range want {
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if got := fi.Mode().Perm(); got != m {
			t.Errorf("%s: mode %04o, want %04o", p, got, m)
		}
	}
}

// TestUnsealProfileTightensExistingDirs: a pre-existing, too-open relay/ or
// users/<name>/ is tightened to 0700 when the profile is unsealed over it.
func TestUnsealProfileTightensExistingDirs(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	alice := filepath.Join(config.UsersDir(), "alice")
	for _, d := range []string{config.RelayDir(), alice} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bundle := buildSealedBundle(t, map[string]string{
		"config.yaml":          "mode: relay\n",
		"relay/main.tf":        "tf",
		"users/alice/user.crt": "CRT",
	})
	if err := unsealProfile(bundle); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{config.RelayDir(), config.UsersDir(), alice} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o700 {
			t.Errorf("%s: mode %04o, want 0700", d, got)
		}
	}
}
