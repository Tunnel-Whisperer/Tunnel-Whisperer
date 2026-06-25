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
}
