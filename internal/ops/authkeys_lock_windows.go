//go:build windows

package ops

import "sync"

// authorizedKeysMu stands in for the flock the other platforms take.
// It serialises authorized_keys writers inside this process only; a second
// tw process (e.g. a CLI invite while the daemon runs) is not excluded on
// Windows.
var authorizedKeysMu sync.Mutex

// lockAuthorizedKeys takes the process-wide authorized_keys mutex; lockPath
// is unused on Windows.
func lockAuthorizedKeys(lockPath string) (release func(), err error) {
	authorizedKeysMu.Lock()
	return authorizedKeysMu.Unlock, nil
}
