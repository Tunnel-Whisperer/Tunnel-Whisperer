//go:build !windows

package ops

import (
	"fmt"
	"os"
	"syscall"
)

// lockAuthorizedKeys takes an exclusive flock on lockPath (created 0600 if
// missing, never through a symlink) and returns its release. flock locks
// belong to the open file description, so it serialises goroutines of this
// process as well as other tw processes (CLI, daemon, dashboard).
func lockAuthorizedKeys(lockPath string) (release func(), err error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", lockPath, err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("locking %s: %w", lockPath, err)
	}
	return func() { f.Close() }, nil
}
