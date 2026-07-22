package ops

import (
	"fmt"
)

// killRelayListenerCmd returns a shell command that kills whatever process
// holds a LISTEN socket on the given port on the relay — in practice the
// sshd session serving a tenant's reverse tunnel, which survives an
// authorized_keys rewrite. All tenants (and the admin's own management
// session) share one SSH user, so the kill targets the listener port, never
// a process name. Pure coreutils + awk (no ss/fuser): find the LISTEN
// socket inode in /proc/net/tcp{,6} (state 0A, hex port), then the pid
// whose fd table holds that inode. No listener found is success — the
// tunnel was already down — hence the trailing true.
func killRelayListenerCmd(port int) string {
	return fmt.Sprintf(
		`inos=$(awk '$4=="0A" && $2 ~ /:%04X$/ {print $10}' /proc/net/tcp /proc/net/tcp6); `+
			`for ino in $inos; do for p in /proc/[0-9]*; do `+
			`sudo ls -l "$p/fd" 2>/dev/null | grep -q "socket:\[$ino\]" && sudo kill "${p#/proc/}"; `+
			`done; done; true`, port)
}
