package ops

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// testCAPEM builds a self-signed CA PEM with the given subject CN. The CN
// matters because DecodeJoinRequest now requires the CA subject to equal the
// joining server's id (finding #10).
func testCAPEM(t *testing.T, cn string) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestJoinRequestRoundTrip(t *testing.T) {
	req := &JoinRequest{
		Version: 1, ServerID: "web-01-a1b2c3d4", Hostname: "web-01",
		UUID: "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff", RelayHost: "relay.example.com",
		CACertPEM: testCAPEM(t, "web-01-a1b2c3d4"), SSHPubkey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL9hJa9TvqEr3KjCzjjK9/aSEoZhJW7LV8HfD0VIaLbK user@host",
	}
	b, err := req.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeJoinRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerID != req.ServerID || got.UUID != req.UUID {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestDecodeJoinRequestRejectsBadCA(t *testing.T) {
	req := &JoinRequest{Version: 1, ServerID: "x-1", UUID: "u", CACertPEM: "not a cert", SSHPubkey: "ssh-ed25519 AAAA"}
	b, _ := req.Encode() // Encode should not validate; Decode does
	if _, err := DecodeJoinRequest(b); err == nil {
		t.Error("expected error for invalid CA PEM")
	}
}

// TestDecodeJoinRequestRejectsCAImpersonation is the #10 boundary check: a CA
// whose subject CN does not equal the joining server's id is refused, so a
// tenant cannot register a CA that impersonates another tenant's id in the
// relay's shared trust pool.
func TestDecodeJoinRequestRejectsCAImpersonation(t *testing.T) {
	req := &JoinRequest{
		Version: 1, ServerID: "victim-11112222", UUID: "u",
		CACertPEM: testCAPEM(t, "attacker-99998888"), // CN != ServerID
		SSHPubkey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL9hJa9TvqEr3KjCzjjK9/aSEoZhJW7LV8HfD0VIaLbK user@host",
	}
	b, _ := req.Encode()
	if _, err := DecodeJoinRequest(b); err == nil {
		t.Fatal("a CA whose subject CN != server_id must be rejected")
	}
}

func TestJoinResponseRoundTrip(t *testing.T) {
	resp := &JoinResponse{Version: 1, ServerID: "web-01-a1b2c3d4", RelayHost: "relay.example.com", Path: "/tw/web-01-a1b2c3d4", RemotePort: 20001, SSHUser: "tw"}
	b, err := resp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeJoinResponse(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.RemotePort != 20001 || got.Path != resp.Path {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
