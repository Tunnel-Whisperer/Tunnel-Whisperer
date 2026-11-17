//go:build !windows

package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestAuthorizedKeysLockFile: the sidecar lock is created owner-only and is
// never opened through a symlink.
func TestAuthorizedKeysLockFile(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(config.Dir(), "authorized_keys.lock")
	if err := appendAuthorizedKey([]byte(genPubKey(t)), "a", []int{22}, false); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(lockPath)
	if err != nil || st.Mode().Perm() != 0o600 || !st.Mode().IsRegular() {
		t.Fatalf("lock file = %v (%v), want a regular 0600 file", st, err)
	}

	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatal(err)
	}
	if err := appendAuthorizedKey([]byte(genPubKey(t)), "b", []int{22}, false); err == nil {
		t.Fatal("authorized_keys was updated through a symlinked lock file")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("symlink target was created: %v", err)
	}
}
