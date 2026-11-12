package ops

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// TestDeleteUserDisconnectsLiveSession is the C4 regression: deleting a user
// while their SSH connection to the in-process server is open must close that
// connection, so no further direct-tcpip channels can be opened on it.
func TestDeleteUserDisconnectsLiveSession(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}

	svc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
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
	svcPort := svc.Addr().(*net.TCPAddr).Port

	privPEM, pub, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	userDir := filepath.Join(config.UsersDir(), "bob")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "id_ed25519.pub"), pub, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendAuthorizedKey(pub, "bob", []int{svcPort}, false); err != nil {
		t.Fatal(err)
	}

	sshPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := twssh.NewServer(sshPort, config.HostKeyDir(), config.AuthorizedKeysPath())
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Run() }()
	defer srv.Stop()

	o := &Ops{cfg: &config.Config{}, srv: serverManager{state: StateRunning, sshSrv: srv}}

	var client *gossh.Client
	for i := 0; i < 200; i++ {
		client, err = gossh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", sshPort), &gossh.ClientConfig{
			User:            "tw",
			Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
			HostKeyCallback: gossh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		})
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	readSvc := func() error {
		c, err := client.Dial("tcp", svc.Addr().String())
		if err != nil {
			return err
		}
		defer c.Close()
		got, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			return err
		}
		if got != "DUMMY-SVC\n" {
			return fmt.Errorf("unexpected reply %q", got)
		}
		return nil
	}
	if err := readSvc(); err != nil {
		t.Fatalf("channel before delete must work: %v", err)
	}

	if err := o.DeleteUser("bob"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { _ = client.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DeleteUser must close the user's live SSH connection")
	}
	if err := readSvc(); err == nil {
		t.Fatal("channel open on the deleted user's connection must fail")
	}
}

// TestUserNameFunctionsRejectTraversal is the C10 regression: names that are
// not [A-Za-z0-9_-]+ are rejected before any path under users/ is touched.
func TestUserNameFunctionsRejectTraversal(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.UsersDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(config.Dir(), "config.yaml")
	if err := os.WriteFile(sentinel, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := &Ops{cfg: &config.Config{}, srv: serverManager{state: StateStopped}}

	for _, name := range []string{"..", ".", "../x", "", "a/b", "bob\n"} {
		if err := o.DeleteUser(name); err == nil {
			t.Errorf("DeleteUser(%q) must fail", name)
		}
		if err := o.SetUserSingleSession(name, true); err == nil {
			t.Errorf("SetUserSingleSession(%q) must fail", name)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("config dir must be intact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.Dir(), ".single-session")); !os.IsNotExist(err) {
		t.Fatal("SetUserSingleSession must not write outside users/")
	}
}

// TestServerXrayPort is the C11 regression: the online-status tunnel must dial
// the same port the server's Xray binds, honoring server.xray_port.
func TestServerXrayPort(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.SSHPort = 2222
	if got := serverXrayPort(cfg); got != 2223 {
		t.Errorf("default = %d, want 2223", got)
	}
	cfg.Server.XrayPort = 40000
	if got := serverXrayPort(cfg); got != 40000 {
		t.Errorf("with xray_port = %d, want 40000", got)
	}
}

// TestRelayTunnelHostKeyCallbackPins: with a pinned relay host key, only that
// key is accepted; without one, the legacy unpinned behavior remains.
func TestRelayTunnelHostKeyCallbackPins(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}

	_, otherPub, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	other, _, _, _, err := gossh.ParseAuthorizedKey(otherPub)
	if err != nil {
		t.Fatal(err)
	}

	cb, algos, err := relayTunnelHostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if algos != nil || cb("relay", addr, other) != nil {
		t.Fatal("without a pin the callback keeps the legacy accept-any behavior")
	}

	_, pinnedLine, err := ensureRelayHostKey()
	if err != nil {
		t.Fatal(err)
	}
	pinned, _, _, _, err := gossh.ParseAuthorizedKey([]byte(pinnedLine))
	if err != nil {
		t.Fatal(err)
	}
	cb, algos, err = relayTunnelHostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if len(algos) != 1 || algos[0] != pinned.Type() {
		t.Errorf("host key algorithms = %v, want [%s]", algos, pinned.Type())
	}
	if err := cb("relay", addr, pinned); err != nil {
		t.Errorf("pinned key must be accepted: %v", err)
	}
	if err := cb("relay", addr, other); err == nil {
		t.Error("a key that does not match the pin must be rejected")
	}

	if err := os.WriteFile(relayHostKeyPubPath(), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := relayTunnelHostKeyCallback(); err == nil {
		t.Error("an unreadable pin must fail closed, not fall back to accept-any")
	}
}

// TestIsCanonicalUUID covers the guard in addUUIDToRelay/addMultipleUUIDsToRelay.
func TestIsCanonicalUUID(t *testing.T) {
	for _, s := range []string{
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		"00000000-0000-0000-0000-000000000000",
	} {
		if !isCanonicalUUID(s) {
			t.Errorf("isCanonicalUUID(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"",
		"6BA7B810-9DAD-11D1-80B4-00C04FD430C8",
		"6ba7b8109dad11d180b400c04fd430c8",
		"{6ba7b810-9dad-11d1-80b4-00c04fd430c8}",
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8\n",
		"6ba7b810-9dad-11d1-80b4-00c04fd430cg",
		"6ba7b810-9dad-11d1-80b4_00c04fd430c8",
		`6ba7b810-9dad-11d1-80b4-00c04fd4","x`,
		strings.Repeat("a", 36),
	} {
		if isCanonicalUUID(s) {
			t.Errorf("isCanonicalUUID(%q) = true, want false", s)
		}
	}
}

// TestAddUUIDToRelayRejectsNonCanonical: the guard fires before any relay
// connection is attempted.
func TestAddUUIDToRelayRejectsNonCanonical(t *testing.T) {
	cfg := &config.Config{}
	if err := addUUIDToRelay(cfg, `x","level":0`); err == nil || !strings.Contains(err.Error(), "non-canonical UUID") {
		t.Errorf("addUUIDToRelay: err = %v, want non-canonical UUID error", err)
	}
	if err := addMultipleUUIDsToRelay(cfg, []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8", "bad"}); err == nil || !strings.Contains(err.Error(), "non-canonical UUID") {
		t.Errorf("addMultipleUUIDsToRelay: err = %v, want non-canonical UUID error", err)
	}
}
