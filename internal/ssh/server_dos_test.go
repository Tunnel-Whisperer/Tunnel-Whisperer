package ssh

import (
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// TestServerBindsLoopback is the SP-11 regression: the embedded SSH server must
// bind 127.0.0.1, not all interfaces. It also confirms the handshake path still
// runs (the server sends its banner) under the new deadline + semaphore.
func TestServerBindsLoopback(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(0, dir, filepath.Join(dir, "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Run() }()
	defer s.Stop()

	var addr net.Addr
	for i := 0; i < 200; i++ {
		if s.listener != nil {
			addr = s.listener.Addr()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if addr == nil {
		t.Fatal("SSH server did not start listening")
	}
	ta, ok := addr.(*net.TCPAddr)
	if !ok || !ta.IP.IsLoopback() {
		t.Fatalf("SSH server must bind loopback, got %v", addr)
	}

	// The handshake path runs: a raw connection receives the SSH banner.
	c, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("expected SSH banner, read error: %v", err)
	}
	if string(buf) != "SSH-" {
		t.Fatalf("expected SSH banner, got %q", buf)
	}
}

// TestHandleConnectionDropsWhenSaturated is the SP-10 regression: when every
// pre-auth handshake slot is taken, a new connection is dropped immediately
// (closed without entering the handshake) instead of spawning an unbounded
// goroutine — the anti-Slowloris / anti-flood behavior.
func TestHandleConnectionDropsWhenSaturated(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(0, dir, filepath.Join(dir, "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	// Saturate the pre-auth handshake slots.
	for i := 0; i < maxConcurrentHandshakes; i++ {
		s.handshakeSem <- struct{}{}
	}

	cli, srv := net.Pipe()
	done := make(chan struct{})
	go func() { s.handleConnection(srv); close(done) }()

	select {
	case <-done:
		// good — dropped without blocking on a handshake
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection must drop immediately when handshakes are saturated")
	}

	// The dropped connection is closed by the server.
	_ = cli.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := cli.Read(make([]byte, 1)); err == nil {
		t.Fatal("a dropped connection must be closed")
	}
	_ = cli.Close()
}

// TestParseDirectTCPIPRejectsOverflow is the SP-13 regression: a huge attacker
// length must be rejected, not wrap a 32-bit sum and slice out of bounds.
func TestParseDirectTCPIPRejectsOverflow(t *testing.T) {
	// hostLen = 0xFFFFFFFF with only a few bytes of data.
	data := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x50}
	if _, err := parseDirectTCPIP(data); err == nil {
		t.Fatal("a length that exceeds the buffer must be rejected")
	}
	// A well-formed payload still parses.
	good := []byte{}
	good = append(good, 0, 0, 0, 3)      // hostLen=3
	good = append(good, 'a', 'b', 'c')   // host
	good = append(good, 0, 0, 0x1F, 0x90) // port 8080
	good = append(good, 0, 0, 0, 1)      // origHostLen=1
	good = append(good, 'x')             // origin host
	good = append(good, 0, 0, 0, 42)     // origin port
	d, err := parseDirectTCPIP(good)
	if err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if d.DestHost != "abc" || d.DestPort != 8080 {
		t.Fatalf("parsed = %+v, want abc:8080", d)
	}
}

// TestIsPortAllowedFailsClosed is the SP-20 regression: no permitopen means
// forward nowhere, not everywhere.
func TestIsPortAllowedFailsClosed(t *testing.T) {
	if isPortAllowed(nil, "127.0.0.1", 5432) {
		t.Error("nil perms must not allow forwarding")
	}
	noPermit := &gossh.Permissions{Extensions: map[string]string{"tw_user": "alice"}}
	if isPortAllowed(noPermit, "127.0.0.1", 5432) {
		t.Error("a key with no permitopen must not allow forwarding")
	}
	withPermit := &gossh.Permissions{Extensions: map[string]string{"permitopen": "127.0.0.1:5432"}}
	if !isPortAllowed(withPermit, "127.0.0.1", 5432) {
		t.Error("a matching permitopen must allow forwarding")
	}
	if isPortAllowed(withPermit, "127.0.0.1", 22) {
		t.Error("a non-matching port must be denied")
	}
}
