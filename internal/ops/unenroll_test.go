package ops

import (
	"os/exec"
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
	// A kill that does not actually free the port must surface as an error,
	// not be swallowed by a trailing `true`: the command re-checks the
	// listener after killing and exits 1 if it survived. No listener found
	// at the start still exits 0 (tunnel already down = success).
	if strings.HasSuffix(strings.TrimSpace(cmd), "true") {
		t.Errorf("kill failure must not be masked by a trailing 'true':\n%s", cmd)
	}
	if !strings.Contains(cmd, "still listening") || !strings.Contains(cmd, "exit 1") {
		t.Errorf("command must re-check the listener and fail loudly if it survives:\n%s", cmd)
	}
	// The command must be valid shell.
	c := exec.Command("sh", "-n")
	c.Stdin = strings.NewReader(cmd)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("command does not parse as shell: %v\n%s\n%s", err, out, cmd)
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
