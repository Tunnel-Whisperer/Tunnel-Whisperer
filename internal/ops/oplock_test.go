package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestEnrollLockMutualExclusion(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())

	release, err := acquireEnrollLock()
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := acquireEnrollLock(); err == nil {
		t.Fatal("second acquire must fail while the lock is held")
	} else if !strings.Contains(err.Error(), "in progress") {
		t.Errorf("lock error should say another operation is in progress, got: %v", err)
	}
	release()
	release2, err := acquireEnrollLock()
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	release2()
}

func TestEnrollLockStealsStaleLock(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())

	// A lock file left behind by a crashed process (old mtime) must not
	// block forever — it is stolen.
	path := filepath.Join(config.Dir(), "enroll.lock")
	if err := os.WriteFile(path, []byte("12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := acquireEnrollLock()
	if err != nil {
		t.Fatalf("stale lock must be stolen, got: %v", err)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("release must remove the lock file, stat err = %v", err)
	}
}
