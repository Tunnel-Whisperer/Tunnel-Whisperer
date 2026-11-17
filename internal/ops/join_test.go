package ops

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/pki"
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
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

const testJoinUUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"

// validJoinRequest returns a join request DecodeJoinRequest accepts.
func validJoinRequest(t *testing.T) *JoinRequest {
	t.Helper()
	return &JoinRequest{
		Version: 1, ServerID: "web-01-a1b2c3d4", Hostname: "web-01",
		UUID: testJoinUUID, RelayHost: "relay.example.com",
		CACertPEM: testCAPEM(t, "web-01-a1b2c3d4"), SSHPubkey: genPubKey(t),
	}
}

func decodeJoin(t *testing.T, r *JoinRequest) (*JoinRequest, error) {
	t.Helper()
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return DecodeJoinRequest(b)
}

// TestDecodeJoinRequestRejectsBadUUID: a non-canonical uuid used to be
// registered and then break every later relay Xray render (C7).
func TestDecodeJoinRequestRejectsBadUUID(t *testing.T) {
	for _, bad := range []string{"not-a-uuid", "a1b2c3d4", "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff\n", "a1b2c3d4aaaabbbbccccddddeeeeffff"} {
		r := validJoinRequest(t)
		r.UUID = bad
		if _, err := decodeJoin(t, r); err == nil || !strings.Contains(err.Error(), "uuid") {
			t.Errorf("uuid %q: err = %v, want uuid rejection", bad, err)
		}
	}
	if _, err := decodeJoin(t, validJoinRequest(t)); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestDecodeJoinRequestServerIDLength(t *testing.T) {
	r := validJoinRequest(t)
	r.ServerID = strings.Repeat("a", maxServerIDLen)
	r.CACertPEM = testCAPEM(t, r.ServerID)
	if _, err := decodeJoin(t, r); err != nil {
		t.Fatalf("%d-char server_id rejected: %v", maxServerIDLen, err)
	}
	r.ServerID = strings.Repeat("a", maxServerIDLen+1)
	r.CACertPEM = testCAPEM(t, r.ServerID)
	if _, err := decodeJoin(t, r); err == nil {
		t.Fatalf("%d-char server_id accepted", maxServerIDLen+1)
	}
}

// TestDecodeJoinRequestRejectsUUIDShapedServerID: a UUID-shaped server_id is
// the email of the tenant's own relay client, and the tenant could add a
// client with that uuid (email = uuid), giving its inbound duplicate emails
// that stop the shared relay's Xray from loading its config.
func TestDecodeJoinRequestRejectsUUIDShapedServerID(t *testing.T) {
	r := validJoinRequest(t)
	r.ServerID = "dddddddd-4444-4444-8444-444444444444"
	r.CACertPEM = testCAPEM(t, r.ServerID)
	if _, err := decodeJoin(t, r); err == nil || !strings.Contains(err.Error(), "must not be shaped like a UUID") {
		t.Fatalf("UUID-shaped server_id: err = %v, want a UUID-shape refusal", err)
	}
	// One character off the UUID shape is an ordinary id.
	r.ServerID = "dddddddd-4444-4444-8444-44444444444"
	r.CACertPEM = testCAPEM(t, r.ServerID)
	if _, err := decodeJoin(t, r); err != nil {
		t.Fatalf("non-UUID server_id rejected: %v", err)
	}
}

// TestDecodeJoinRequestRejectsTrailingPEM: only the first block was validated
// but the whole string was stored and installed into the relay trust pool, so
// a tenant could smuggle a second CA (CN=<victim>) in after its own.
func TestDecodeJoinRequestRejectsTrailingPEM(t *testing.T) {
	own := testCAPEM(t, "web-01-a1b2c3d4")
	for name, pemStr := range map[string]string{
		"second CA":     own + testCAPEM(t, "victim-11112222"),
		"trailing junk": own + "junk\n",
		"private key":   own + "-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n",
	} {
		r := validJoinRequest(t)
		r.CACertPEM = pemStr
		if _, err := decodeJoin(t, r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	r := validJoinRequest(t)
	r.CACertPEM = "leading text\n" + own + "\n\n  \n"
	got, err := decodeJoin(t, r)
	if err != nil {
		t.Fatalf("single certificate with surrounding whitespace rejected: %v", err)
	}
	if got.CACertPEM != own {
		t.Fatalf("ca_cert_pem not canonicalized to the validated block:\n%q\nwant\n%q", got.CACertPEM, own)
	}
}

// TestDecodeJoinRequestRequiresPathLenZero: a tenant CA without a path length
// limit could sign an intermediate named after another tenant and pass the
// relay's subject/issuer string checks; CAs from pki.GenerateCA must pass.
func TestDecodeJoinRequestRequiresPathLenZero(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mk := func(tmpl *x509.Certificate) string {
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	base := func() *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "web-01-a1b2c3d4"},
			NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		}
	}
	unlimited := base()
	pathLen1 := base()
	pathLen1.MaxPathLen = 1
	for name, c := range map[string]string{"unlimited": mk(unlimited), "pathlen 1": mk(pathLen1)} {
		r := validJoinRequest(t)
		r.CACertPEM = c
		if _, err := decodeJoin(t, r); err == nil || !strings.Contains(err.Error(), "path length") {
			t.Errorf("%s: err = %v, want path-length rejection", name, err)
		}
	}

	genCA, _, err := pki.GenerateCA("web-01-a1b2c3d4")
	if err != nil {
		t.Fatal(err)
	}
	r := validJoinRequest(t)
	r.CACertPEM = string(genCA)
	if _, err := decodeJoin(t, r); err != nil {
		t.Fatalf("pki.GenerateCA CA rejected: %v", err)
	}
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
	req := &JoinRequest{Version: 1, ServerID: "x-1", UUID: testJoinUUID, CACertPEM: "not a cert", SSHPubkey: "ssh-ed25519 AAAA"}
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
		Version: 1, ServerID: "victim-11112222", UUID: testJoinUUID,
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
