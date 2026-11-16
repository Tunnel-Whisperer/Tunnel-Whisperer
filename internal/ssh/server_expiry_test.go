package ssh

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// expiryFixture is an embedded server with users "bob" and "carol", both
// permitted to a loopback dummy service, whose UserExpired callback is set
// before the server starts.
type expiryFixture struct {
	addr    string
	svcAddr string
	signers map[string]gossh.Signer
}

func newExpiryFixture(t *testing.T, userExpired func(string) bool) *expiryFixture {
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

	f := &expiryFixture{svcAddr: svc.Addr().String(), signers: map[string]gossh.Signer{}}
	var ak strings.Builder
	for _, name := range []string{"bob", "carol"} {
		privPEM, pub, err := GenerateKeyPair()
		if err != nil {
			t.Fatal(err)
		}
		signer, err := gossh.ParsePrivateKey(privPEM)
		if err != nil {
			t.Fatal(err)
		}
		f.signers[name] = signer
		fmt.Fprintf(&ak, "permitopen=%q %s %s@tw\n", f.svcAddr, strings.TrimSpace(string(pub)), name)
	}
	akPath := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(akPath, []byte(ak.String()), 0600); err != nil {
		t.Fatal(err)
	}

	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(port, dir, akPath)
	if err != nil {
		t.Fatal(err)
	}
	s.UserExpired = userExpired
	go func() { _ = s.Run() }()
	t.Cleanup(func() { s.Stop() })
	f.addr = fmt.Sprintf("127.0.0.1:%d", port)

	// Wait until the server accepts TCP connections.
	for i := 0; i < 200; i++ {
		c, err := net.Dial("tcp", f.addr)
		if err == nil {
			c.Close()
			return f
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("SSH server did not start listening")
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (f *expiryFixture) dial(name string) (*gossh.Client, error) {
	return gossh.Dial("tcp", f.addr, &gossh.ClientConfig{
		User:            "tw",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(f.signers[name])},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

func (f *expiryFixture) readService(client *gossh.Client) error {
	c, err := client.Dial("tcp", f.svcAddr)
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

func TestAuthRefusedWhenUserExpired(t *testing.T) {
	f := newExpiryFixture(t, func(u string) bool { return u == "bob" })

	client, err := f.dial("bob")
	if err == nil {
		client.Close()
		t.Fatal("expired user bob must not authenticate")
	}
	if !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("bob dial error = %v, want an authentication failure", err)
	}

	client, err = f.dial("carol")
	if err != nil {
		t.Fatalf("unexpired user carol must authenticate: %v", err)
	}
	defer client.Close()
	if err := f.readService(client); err != nil {
		t.Fatalf("carol's channel must work: %v", err)
	}
}

func TestChannelRefusedWhenUserExpiresMidSession(t *testing.T) {
	var mu sync.Mutex
	expired := false
	f := newExpiryFixture(t, func(u string) bool {
		mu.Lock()
		defer mu.Unlock()
		return u == "bob" && expired
	})

	client, err := f.dial("bob")
	if err != nil {
		t.Fatalf("bob must authenticate before expiry: %v", err)
	}
	defer client.Close()
	if err := f.readService(client); err != nil {
		t.Fatalf("channel before expiry must work: %v", err)
	}

	mu.Lock()
	expired = true
	mu.Unlock()

	err = f.readService(client)
	if err == nil {
		t.Fatal("new channel on an expired user's connection must be refused")
	}
	if !strings.Contains(err.Error(), "administratively prohibited") {
		t.Fatalf("channel error = %v, want administratively prohibited", err)
	}
}

func TestNilUserExpiredKeepsOldBehaviour(t *testing.T) {
	f := newExpiryFixture(t, nil)

	for _, name := range []string{"bob", "carol"} {
		client, err := f.dial(name)
		if err != nil {
			t.Fatalf("%s must authenticate with a nil UserExpired: %v", name, err)
		}
		if err := f.readService(client); err != nil {
			client.Close()
			t.Fatalf("%s's channel must work with a nil UserExpired: %v", name, err)
		}
		client.Close()
	}
}
