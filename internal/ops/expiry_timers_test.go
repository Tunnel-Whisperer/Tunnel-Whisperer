package ops

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/enroll"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// syncBuffer is a bytes.Buffer safe for the slog handler and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// expiryEnv is an Ops with an in-process twssh server and a dummy service the
// granted users may open direct-tcpip channels to.
type expiryEnv struct {
	o       *Ops
	sshPort int
	svcAddr string
	svcPort int
}

func newExpiryEnv(t *testing.T) *expiryEnv {
	t.Helper()
	return startExpiryEnv(t, nil)
}

// startExpiryEnv is newExpiryEnv with the SSH server's UserExpired callback
// set before it starts.
func startExpiryEnv(t *testing.T, userExpired func(string) bool) *expiryEnv {
	t.Helper()
	o := newExpiryOps(t)

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

	sshPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := twssh.NewServer(sshPort, config.HostKeyDir(), config.AuthorizedKeysPath())
	if err != nil {
		t.Fatal(err)
	}
	srv.UserExpired = userExpired
	go func() { _ = srv.Run() }()
	t.Cleanup(func() { _ = srv.Stop() })

	o.srv = serverManager{state: StateRunning, sshSrv: srv}
	return &expiryEnv{o: o, sshPort: sshPort, svcAddr: svc.Addr().String(), svcPort: svc.Addr().(*net.TCPAddr).Port}
}

// grant creates name through grantClient (cert NotAfter = exp, SSH key in
// authorized_keys permitted to the dummy service) and returns its SSH signer.
func (e *expiryEnv) grant(t *testing.T, name string, exp time.Time) gossh.Signer {
	t.Helper()
	var c clientMaterial
	if _, err := c.makeOffer(name); err != nil {
		t.Fatal(err)
	}
	off := enroll.ClientOffer{Username: name, UUID: c.uuid, SSHPubkey: string(c.sshPub), CSRPEM: string(c.csrPEM)}
	req := CreateUserRequest{
		Name:      name,
		Mappings:  []config.PortMapping{{ClientPort: 18080, ServerPort: e.svcPort}},
		ExpiresAt: exp,
	}
	if _, err := grantAnyExpiry(e.o, req, &off); err != nil {
		t.Fatalf("grantClient(%s): %v", name, err)
	}
	signer, err := gossh.ParsePrivateKey(c.sshPriv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func (e *expiryEnv) connect(t *testing.T, signer gossh.Signer) *gossh.Client {
	t.Helper()
	var client *gossh.Client
	var err error
	for i := 0; i < 200; i++ {
		client, err = gossh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", e.sshPort), &gossh.ClientConfig{
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
	t.Cleanup(func() { client.Close() })
	return client
}

func (e *expiryEnv) readSvc(client *gossh.Client) error {
	c, err := client.Dial("tcp", e.svcAddr)
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

func waitClosed(t *testing.T, client *gossh.Client, d time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = client.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func expiredLogged(logs, name string) bool {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "user credential expired, sessions disconnected") && strings.Contains(line, "user="+name) {
			return true
		}
	}
	return false
}

func TestExpiryTimerDisconnectsLiveSession(t *testing.T) {
	logs := captureSlog(t)
	e := startExpiryEnv(t, userExpiredNow)
	signer := e.grant(t, "bob", time.Now().Add(5*time.Second))
	client := e.connect(t, signer)
	if err := e.readSvc(client); err != nil {
		t.Fatalf("channel before expiry must work: %v", err)
	}

	w := newExpiryWatcher(e.o, time.Hour)
	w.Start()
	defer w.Stop()
	if w.pending() != 1 {
		t.Fatalf("pending = %d, want 1", w.pending())
	}

	if !waitClosed(t, client, 15*time.Second) {
		t.Fatal("expiry timer must close bob's live SSH connection")
	}
	if err := e.readSvc(client); err == nil {
		t.Fatal("channel open on the expired user's connection must fail")
	}
	if !waitFor(t, 2*time.Second, func() bool { return expiredLogged(logs.String(), "bob") }) {
		t.Fatalf("missing expiry log for bob; logs:\n%s", logs.String())
	}
	if w.pending() != 0 {
		t.Fatalf("pending after fire = %d, want 0", w.pending())
	}

	// The reconnect the e2e saw: the same key must no longer authenticate.
	again, err := gossh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", e.sshPort), &gossh.ClientConfig{
		User:            "tw",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err == nil {
		again.Close()
		t.Fatal("expired bob must not re-authenticate after the timer disconnected him")
	}
	if !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("reconnect error = %v, want an authentication failure", err)
	}
}

func TestExpiryRescanDisconnectsAlreadyExpired(t *testing.T) {
	e := newExpiryEnv(t)
	signer := e.grant(t, "carol", time.Now().Add(-30*time.Minute))
	keep := e.grant(t, "dave", time.Now().Add(time.Hour))
	client := e.connect(t, signer)
	other := e.connect(t, keep)
	if err := e.readSvc(client); err != nil {
		t.Fatalf("expired user's SSH key must still authenticate before rescan: %v", err)
	}

	w := newExpiryWatcher(e.o, time.Hour)
	w.Rescan()
	defer w.Stop()

	if !waitClosed(t, client, 5*time.Second) {
		t.Fatal("Rescan must close the already-expired user's connection")
	}
	if err := e.readSvc(other); err != nil {
		t.Fatalf("unexpired user must stay connected: %v", err)
	}
	if w.pending() != 1 {
		t.Fatalf("pending = %d, want 1 (dave only)", w.pending())
	}
}

func TestExpiryTimerDroppedOnDelete(t *testing.T) {
	logs := captureSlog(t)
	e := newExpiryEnv(t)
	e.grant(t, "bob", time.Now().Add(5*time.Second))
	// No relay is configured; drop the client config so DeleteUser does not
	// try to reach one for the UUID.
	if err := os.Remove(filepath.Join(config.UsersDir(), "bob", "config.yaml")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	w := newExpiryWatcher(e.o, time.Hour)
	e.o.srv.expiry = w
	w.Start()
	defer w.Stop()
	if w.pending() != 1 {
		t.Fatalf("pending = %d, want 1", w.pending())
	}

	if err := e.o.DeleteUser("bob"); err != nil {
		t.Fatal(err)
	}
	if w.pending() != 0 {
		t.Fatalf("pending after delete = %d, want 0", w.pending())
	}
	time.Sleep(7 * time.Second)
	if expiredLogged(logs.String(), "bob") {
		t.Fatalf("deleted user's timer fired; logs:\n%s", logs.String())
	}
}

// TestExpiryTimerRechecksDiskBeforeDisconnect: a timer armed for an expiry
// that no longer holds (re-issued or deleted by another process, no Forget)
// must neither disconnect nor log.
func TestExpiryTimerRechecksDiskBeforeDisconnect(t *testing.T) {
	logs := captureSlog(t)
	e := newExpiryEnv(t)
	signer := e.grant(t, "bob", time.Now().Add(5*time.Second))
	e.grant(t, "carol", time.Now().Add(5*time.Second))
	e.grant(t, "renewed", time.Now().Add(48*time.Hour))
	client := e.connect(t, signer)

	w := newExpiryWatcher(e.o, time.Hour)
	w.Start()
	defer w.Stop()
	if w.pending() != 3 {
		t.Fatalf("pending = %d, want 3", w.pending())
	}

	renewed, err := os.ReadFile(filepath.Join(config.UsersDir(), "renewed", "client.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.UsersDir(), "bob", "client.crt"), renewed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(config.UsersDir(), "carol")); err != nil {
		t.Fatal(err)
	}

	if !waitFor(t, 10*time.Second, func() bool { return w.pending() == 1 }) {
		t.Fatalf("bob's and carol's timers must have fired; pending = %d", w.pending())
	}
	if err := e.readSvc(client); err != nil {
		t.Fatalf("re-issued bob must stay connected: %v", err)
	}
	if expiredLogged(logs.String(), "bob") || expiredLogged(logs.String(), "carol") {
		t.Fatalf("stale timer acted; logs:\n%s", logs.String())
	}
}

// TestNoCertUserStillConnects: a user enrolled before the feature (no
// client.crt, only id_ed25519.pub and its authorized_keys line) must still
// authenticate and open channels with the real UserExpired wiring.
func TestNoCertUserStillConnects(t *testing.T) {
	e := startExpiryEnv(t, userExpiredNow)
	var c clientMaterial
	if _, err := c.makeOffer("legacy"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(config.UsersDir(), "legacy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519.pub"), c.sshPub, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendAuthorizedKey(c.sshPub, "legacy", []int{e.svcPort}, false); err != nil {
		t.Fatal(err)
	}
	if userExpiredNow("legacy") {
		t.Fatal("userExpiredNow(legacy) = true for a user without client.crt")
	}
	signer, err := gossh.ParsePrivateKey(c.sshPriv)
	if err != nil {
		t.Fatal(err)
	}
	client := e.connect(t, signer)
	if err := e.readSvc(client); err != nil {
		t.Fatalf("channel for a user without client.crt must work: %v", err)
	}
}

func TestExpiryStartTwice(t *testing.T) {
	o := newExpiryOps(t)
	w := newExpiryWatcher(o, time.Hour)
	w.Start()
	w.Start()
	w.Stop()
}

func TestExpiryForgetNilWatcher(t *testing.T) {
	var w *expiryWatcher
	w.Forget("bob")
}

func TestExpiryStopBeforeStart(t *testing.T) {
	o := newExpiryOps(t)
	grantForTest(t, o, "bob", time.Now().Add(time.Hour))
	w := newExpiryWatcher(o, time.Hour)
	w.Stop()
	w.Start()
	if w.pending() != 0 {
		t.Fatalf("pending = %d, want 0 after Stop", w.pending())
	}
	w.Stop()
}
