package ops

import (
	"strings"
	"testing"
)

func TestKillRelayListenerCmd(t *testing.T) {
	cmd := killRelayListenerCmd(20001)
	// 20001 = 0x4E21 — the /proc/net/tcp local_address port is hex.
	if !strings.Contains(cmd, `/:4E21$/`) {
		t.Errorf("command does not match hex port 4E21:\n%s", cmd)
	}
	if !strings.Contains(cmd, "/proc/net/tcp") || !strings.Contains(cmd, "/proc/net/tcp6") {
		t.Errorf("command must read /proc/net/tcp and /proc/net/tcp6:\n%s", cmd)
	}
	// No listener found must still exit 0 (tunnel already down = success).
	if !strings.HasSuffix(cmd, "true") {
		t.Errorf("command must end in 'true' so no-match is not an error:\n%s", cmd)
	}
}
