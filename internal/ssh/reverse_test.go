package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// TestReverseTunnelFallbackUser: a server whose stored relay user no longer
// accepts its key (pre-migration admin account) retries once with
// FallbackUser, reports the switch, and keeps the new user afterwards.
func TestReverseTunnelFallbackUser(t *testing.T) {
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := gossh.NewSignerFromKey(hostPriv)
	var mu sync.Mutex
	var attempts []string
	sc := &gossh.ServerConfig{
		PublicKeyCallback: func(c gossh.ConnMetadata, _ gossh.PublicKey) (*gossh.Permissions, error) {
			mu.Lock()
			attempts = append(attempts, c.User())
			mu.Unlock()
			if c.User() != "twt-0123456789ab" {
				return nil, io.EOF
			}
			return nil, nil
		},
	}
	sc.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := gossh.NewServerConn(nc, sc)
				if err != nil {
					return
				}
				go func() {
					for nch := range chans {
						nch.Reject(gossh.Prohibited, "no channels")
					}
				}()
				for req := range reqs {
					if req.Type == "tcpip-forward" {
						port := make([]byte, 4)
						binary.BigEndian.PutUint32(port, 20000)
						req.Reply(true, port)
						continue
					}
					req.Reply(false, nil)
				}
			}()
		}
	}()

	priv, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, priv, 0o600); err != nil {
		t.Fatal(err)
	}
	switched := make(chan string, 1)
	rt := &ReverseTunnel{
		RemoteAddr:   ln.Addr().String(),
		User:         "ubuntu",
		KeyPath:      keyPath,
		RemotePort:   20000,
		LocalAddr:    "127.0.0.1:1",
		FallbackUser: "twt-0123456789ab",
		OnUserSwitch: func(u string) { switched <- u },
	}
	go rt.Run()
	defer rt.Stop()

	select {
	case u := <-switched:
		if u != "twt-0123456789ab" {
			t.Fatalf("switched to %q", u)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("tunnel never switched users (last error %q)", rt.LastError())
	}
	deadline := time.Now().Add(5 * time.Second)
	for !rt.Connected() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !rt.Connected() {
		t.Fatalf("tunnel not connected after switching users (last error %q)", rt.LastError())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 || attempts[0] != "ubuntu" || attempts[1] != "twt-0123456789ab" {
		t.Errorf("login attempts = %v, want [ubuntu twt-0123456789ab]", attempts)
	}
}
