package ops

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestAuthorizedKeysConcurrentWritersNoLostUpdates: updateAuthorizedKeys used
// to read, edit and rename authorized_keys with no lock, so a writer racing
// removeAuthorizedKey(victim) put its stale snapshot back (the revoked key
// returned) and concurrent appends overwrote each other.
func TestAuthorizedKeysConcurrentWritersNoLostUpdates(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	has := func(content, pub string) bool { return strings.Contains(content, strings.Fields(pub)[1]) }

	const rounds, writers = 50, 3
	resurrected, lost := 0, 0
	for r := 0; r < rounds; r++ {
		if err := os.Remove(config.AuthorizedKeysPath()); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		victim := genPubKey(t)
		if err := appendAuthorizedKey([]byte(victim), "victim", []int{8080}, false); err != nil {
			t.Fatal(err)
		}
		others := make([]string, writers)
		for i := range others {
			others[i] = genPubKey(t)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, writers+1)
		wg.Add(writers + 1)
		go func() { defer wg.Done(); <-start; errs <- removeAuthorizedKey([]byte(victim)) }()
		for _, o := range others {
			go func(p string) {
				defer wg.Done()
				<-start
				errs <- appendAuthorizedKey([]byte(p), "o", []int{22}, false)
			}(o)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		content := readAuthorizedKeys(t)
		if has(content, victim) {
			resurrected++
		}
		for _, o := range others {
			if !has(content, o) {
				lost++
			}
		}
	}
	if resurrected != 0 || lost != 0 {
		t.Fatalf("rounds=%d writers=%d: revoked key resurrected in %d rounds, %d/%d appends lost",
			rounds, writers, resurrected, lost, rounds*writers)
	}
}

// TestEnsureKeysSeedsAuthorizedKeysUnderLock: EnsureKeys seeded
// authorized_keys with Stat-then-WriteFile outside the lock, so a line another
// writer appended in between was overwritten on a fresh install.
func TestEnsureKeysSeedsAuthorizedKeysUnderLock(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.FilePath(), []byte("mode: client\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}

	release, err := lockAuthorizedKeys(authorizedKeysLockPath())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- o.EnsureKeys() }()
	select {
	case err := <-done:
		release()
		t.Fatalf("EnsureKeys finished (err %v) while another writer held the authorized_keys lock", err)
	case <-time.After(300 * time.Millisecond):
	}
	// The other writer appends its line while it holds the lock.
	other, err := authorizedKeyLine([]byte(genPubKey(t)), "other", []int{22}, false)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err := writeFileAtomic(config.AuthorizedKeysPath(), []byte(other+"\n"), 0o600); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("EnsureKeys did not finish after the lock was released")
	}
	if got := readAuthorizedKeys(t); got != other+"\n" {
		t.Fatalf("authorized_keys = %q, want the concurrently written line kept", got)
	}
}
