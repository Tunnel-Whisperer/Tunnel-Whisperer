package ops

import (
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestAddAndListServers(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	o, _ := New()
	a, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "a-1", UUID: "ua", CACertPEM: testCAPEM(t), SSHPubkey: "ssh-ed25519 AAAA"})
	if err != nil {
		t.Fatal(err)
	}
	if a.RemotePort != 20000 {
		t.Errorf("first port = %d, want 20000", a.RemotePort)
	}
	b, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "b-2", UUID: "ub", CACertPEM: testCAPEM(t), SSHPubkey: "ssh-ed25519 BBBB"})
	if err != nil {
		t.Fatal(err)
	}
	if b.RemotePort != 20001 {
		t.Errorf("second port = %d, want 20001", b.RemotePort)
	}
	if _, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "a-1", UUID: "ua2", CACertPEM: testCAPEM(t), SSHPubkey: "ssh-ed25519 CCCC"}); err == nil {
		t.Error("duplicate server-id should be rejected")
	}
	list, _ := o.ListServers()
	if len(list) != 2 {
		t.Fatalf("want 2 servers, got %d", len(list))
	}
	for _, s := range list {
		if s.EnrolledAt == "" {
			t.Errorf("server %s missing EnrolledAt stamp", s.ServerID)
		}
	}
}

func TestParseListeningPorts(t *testing.T) {
	// Realistic concatenated /proc/net/tcp + /proc/net/tcp6 content: hex
	// local ports, state 0A = LISTEN. Includes an ESTABLISHED (01) row and
	// headers, both of which must be ignored.
	out := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:4E20 00000000:0000 0A 00000000:00000000 00:00000000 00000000   112        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:2765 00000000:0000 0A 00000000:00000000 00:00000000 00000000   112        0 12346 1 0000000000000000 100 0 0 10 0
   2: 0100007F:4E21 0100007F:0016 01 00000000:00000000 00:00000000 00000000     0        0 12347 1 0000000000000000 100 0 0 10 0
  sl  local_address                         rem_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12348 1 0000000000000000 100 0 0 10 0
`
	ports := parseListeningPorts(out)
	for _, want := range []int{20000, 10085, 443} { // 0x4E20, 0x2765, 0x01BB
		if !ports[want] {
			t.Errorf("port %d not detected in:\n%s", want, out)
		}
	}
	if ports[20001] { // 0x4E21 is ESTABLISHED, not LISTEN
		t.Error("port 20001 (non-LISTEN row) falsely detected")
	}
}
