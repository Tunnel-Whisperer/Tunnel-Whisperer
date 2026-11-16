package ssh

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// revokeFixture is an embedded server with one user "bob" whose key permits a
// single loopback service, plus an established client connection as bob.
type revokeFixture struct {
	srv     *Server
	akPath  string
	svcAddr string
	client  *gossh.Client
}

func newRevokeFixture(t *testing.T) *revokeFixture {
	t.Helper()
	dir := t.TempDir()

	svc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	go func() {
		for {
			c, err := svc.Accept()
			if err != nil {
				return
			}
			fmt.Fprintln(c, "DUMMY-SVC")
			c.Close()
		}
	}()

	privPEM, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	akPath := filepath.Join(dir, "authorized_keys")
	line := fmt.Sprintf("permitopen=%q %s bob@tw\n", svc.Addr().String(), strings.TrimSpace(string(pub)))
	if err := os.WriteFile(akPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}

	s, err := NewServer(0, dir, akPath)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Run() }()
	t.Cleanup(func() { s.Stop() })
	var addr string
	for i := 0; i < 200 && addr == ""; i++ {
		if a := listenerAddr(s); a != nil {
			addr = a.String()
		} else {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if addr == "" {
		t.Fatal("SSH server did not start listening")
	}

	client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "tw",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })

	f := &revokeFixture{srv: s, akPath: akPath, svcAddr: svc.Addr().String(), client: client}
	if err := f.readService(); err != nil {
		t.Fatalf("channel before revocation must work: %v", err)
	}
	return f
}

// listenerAddr returns the server's bound address, or nil before Run listens.
func listenerAddr(s *Server) net.Addr {
	s.lisMu.Lock()
	defer s.lisMu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (f *revokeFixture) readService() error {
	c, err := f.client.Dial("tcp", f.svcAddr)
	if err != nil {
		return err
	}
	defer c.Close()
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if got != "DUMMY-SVC\n" {
		return fmt.Errorf("unexpected service reply %q", got)
	}
	return nil
}

// TestDirectTCPIPRechecksAuthorizedKeys is the C4 defense-in-depth regression:
// once bob's key line is removed from authorized_keys, new direct-tcpip
// channels on his already-established connection are refused.
func TestDirectTCPIPRechecksAuthorizedKeys(t *testing.T) {
	f := newRevokeFixture(t)

	if err := os.WriteFile(f.akPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.readService(); err == nil {
		t.Fatal("channel on an existing connection must fail after the key is removed")
	}
}

// TestDirectTCPIPRechecksNarrowedPermitOpen: a key whose permitopen no longer
// covers the destination is refused even though the handshake snapshot did.
func TestDirectTCPIPRechecksNarrowedPermitOpen(t *testing.T) {
	f := newRevokeFixture(t)

	data, err := os.ReadFile(f.akPath)
	if err != nil {
		t.Fatal(err)
	}
	narrowed := strings.Replace(string(data), f.svcAddr, "127.0.0.1:1", 1)
	if err := os.WriteFile(f.akPath, []byte(narrowed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.readService(); err == nil {
		t.Fatal("channel must fail once permitopen no longer covers the destination")
	}
}

// TestDisconnectUserClosesLiveConnections is the C4 regression for the
// server side of DeleteUser: DisconnectUser tears down bob's established
// connection and drops it from the session tracker.
func TestDisconnectUserClosesLiveConnections(t *testing.T) {
	f := newRevokeFixture(t)

	if n := f.srv.DisconnectUser("alice"); n != 0 {
		t.Fatalf("DisconnectUser(alice) closed %d connections, want 0", n)
	}
	if n := f.srv.DisconnectUser("bob"); n != 1 {
		t.Fatalf("DisconnectUser(bob) closed %d connections, want 1", n)
	}

	done := make(chan struct{})
	go func() { _ = f.client.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("client connection must be closed by DisconnectUser")
	}
	if err := f.readService(); err == nil {
		t.Fatal("channel open must fail on a disconnected connection")
	}

	for i := 0; i < 200; i++ {
		if len(f.srv.ConnectedUsers()) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session tracker still lists %v after disconnect", f.srv.ConnectedUsers())
}
