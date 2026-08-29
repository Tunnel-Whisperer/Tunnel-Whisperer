package ssh

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// The forward tunnel is the end-to-end SSH session that keeps a semi-trusted
// relay from seeing plaintext, so it must never connect without pinning the
// server's host key.
func TestForwardTunnelHostKeyCallbackFailsClosedWhenEmpty(t *testing.T) {
	ft := &ForwardTunnel{}
	if _, err := ft.hostKeyCallback(); err == nil {
		t.Fatal("empty ServerHostKey must fail closed, got nil error")
	}
}

func TestForwardTunnelHostKeyCallbackRejectsMalformedKey(t *testing.T) {
	ft := &ForwardTunnel{ServerHostKey: "not-a-valid-authorized-key"}
	if _, err := ft.hostKeyCallback(); err == nil {
		t.Fatal("malformed ServerHostKey must return a parse error")
	}
}

func TestForwardTunnelHostKeyCallbackPinsServerKey(t *testing.T) {
	_, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	pinned, _, _, _, err := gossh.ParseAuthorizedKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	ft := &ForwardTunnel{ServerHostKey: string(pub)}
	cb, err := ft.hostKeyCallback()
	if err != nil {
		t.Fatalf("valid ServerHostKey should build a callback: %v", err)
	}

	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2222}
	if err := cb("127.0.0.1:2222", addr, pinned); err != nil {
		t.Fatalf("the pinned host key must be accepted: %v", err)
	}

	// A different key — a relay MITM substituting its own host key — must fail.
	_, otherPub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	other, _, _, _, err := gossh.ParseAuthorizedKey(otherPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := cb("127.0.0.1:2222", addr, other); err == nil {
		t.Fatal("a mismatching host key (MITM) must be rejected")
	}
}

func TestEnsureHostPublicKeyIsStableAndPersists(t *testing.T) {
	dir := t.TempDir()

	k1, err := EnsureHostPublicKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k1, "ssh-ed25519 ") {
		t.Fatalf("want authorized_keys format, got %q", k1)
	}
	if _, err := os.Stat(filepath.Join(dir, "ssh_host_ed25519_key")); err != nil {
		t.Fatalf("host key must be persisted: %v", err)
	}

	// A second call must return the same key, never regenerate it — otherwise
	// the pin handed to clients would not match the running server.
	k2, err := EnsureHostPublicKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Fatal("EnsureHostPublicKey must be stable across calls")
	}
}
