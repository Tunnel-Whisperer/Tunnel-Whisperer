package ops

import (
	"fmt"
	"net"
	"testing"
)

func TestFreeLoopbackPortIsBindable(t *testing.T) {
	p, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		t.Fatalf("returned port %d not bindable: %v", p, err)
	}
	ln.Close()
}

func TestLoopbackPortFree(t *testing.T) {
	// A port we currently hold is not free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	if loopbackPortFree(busy) {
		t.Errorf("loopbackPortFree(%d) = true, want false (port is held)", busy)
	}

	// A fresh OS-assigned port reports free.
	free, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	if !loopbackPortFree(free) {
		t.Errorf("loopbackPortFree(%d) = false, want true", free)
	}
}

// Regression for the fixed-port relay-tunnel collision: every management tunnel
// reused one hard-coded local port (TempXrayPort+1), so a tunnel opened while a
// prior one's listener lingered — or another op was in flight — could not bind
// ("Only one usage of each socket address" on Windows). Per-call free ports must
// be distinct and simultaneously bindable.
func TestConcurrentTunnelPortsDoNotCollide(t *testing.T) {
	p1, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	ln1, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p1))
	if err != nil {
		t.Fatalf("bind p1=%d: %v", p1, err)
	}
	defer ln1.Close()

	p2, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	if p2 == p1 {
		t.Fatalf("freeLoopbackPort returned the in-use port %d", p1)
	}
	ln2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p2))
	if err != nil {
		t.Fatalf("bind p2=%d while p1 held: %v", p2, err)
	}
	ln2.Close()
}
