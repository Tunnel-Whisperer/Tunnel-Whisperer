package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// enrollLockStaleAfter is how old an enroll.lock file must be before it is
// considered abandoned by a crashed process and stolen. Enroll/un-enroll
// finish in seconds; anything this old is not a live operation.
const enrollLockStaleAfter = 15 * time.Minute

// acquireEnrollLock serializes enroll and un-enroll on this admin profile.
// Both operations read the registry, render the FULL relay state from it and
// rewrite authorized_keys/Caddyfile/config.json wholesale — two interleaved
// runs would each render from a snapshot missing the other's change and the
// loser's rewrite would silently drop a tenant. The lock is a plain
// O_EXCL-created file (cross-platform, no flock), removed on release; a
// stale file older than enrollLockStaleAfter is stolen.
func acquireEnrollLock() (release func(), err error) {
	path := filepath.Join(config.Dir(), "enroll.lock")
	for attempt := 0; ; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("creating enroll lock %s: %w", path, err)
		}
		st, statErr := os.Stat(path)
		if statErr == nil && time.Since(st.ModTime()) > enrollLockStaleAfter && attempt == 0 {
			os.Remove(path) // abandoned by a crashed run — steal it
			continue
		}
		held := "recently"
		if statErr == nil {
			held = "since " + st.ModTime().Format(time.RFC3339)
		}
		return nil, fmt.Errorf("another enroll/un-enroll is in progress (lock %s held %s); if no other tw is running, delete the file and retry", path, held)
	}
}
