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

func TestExcludeServer(t *testing.T) {
	in := []RegisteredServer{
		{ServerID: "a-1", RemotePort: 20000},
		{ServerID: "b-2", RemotePort: 20001},
		{ServerID: "c-3", RemotePort: 20002},
	}
	out := excludeServer(in, "b-2")
	if len(out) != 2 || out[0].ServerID != "a-1" || out[1].ServerID != "c-3" {
		t.Errorf("excludeServer(b-2) = %+v, want a-1 and c-3 in order", out)
	}
	if got := excludeServer(in, "nope"); len(got) != 3 {
		t.Errorf("excluding an absent id must keep all %d servers, got %d", len(in), len(got))
	}
}
