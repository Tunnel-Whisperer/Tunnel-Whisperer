package modeauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// testKey returns a PEM-encoded OpenSSH ed25519 private key and its
// authorized-keys public form.
func testKey(t *testing.T) (privPEM []byte, pubAuthorized string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem_bytes(t, pemBlock), string(gossh.MarshalAuthorizedKey(sshPub))
}

func pem_bytes(t *testing.T, b *pem.Block) []byte {
	t.Helper()
	return pem.EncodeToMemory(b)
}

func TestSignVerifyRoundTrip(t *testing.T) {
	priv, pub := testKey(t)
	sig, issuer, err := Sign(priv, "server", pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify("server", pub, sig, issuer); err != nil {
		t.Fatalf("round-trip verify failed: %v", err)
	}
}

func TestVerifyRejectsTamperedMode(t *testing.T) {
	priv, pub := testKey(t)
	sig, issuer, _ := Sign(priv, "server", pub)
	if err := Verify("relay", pub, sig, issuer); err == nil {
		t.Error("verify accepted a mode the signature does not cover")
	}
}

func TestVerifyRejectsTamperedIdentity(t *testing.T) {
	priv, pub := testKey(t)
	_, otherPub := testKey(t)
	sig, issuer, _ := Sign(priv, "client", pub)
	if err := Verify("client", otherPub, sig, issuer); err == nil {
		t.Error("verify accepted a different identity")
	}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	priv, pub := testKey(t)
	_, _ = base64.StdEncoding, ed25519.PublicKey(nil)
	sig, _, _ := Sign(priv, "server", pub)
	otherPriv, _ := testKey(t)
	_, wrongIssuer, _ := Sign(otherPriv, "server", pub)
	if err := Verify("server", pub, sig, wrongIssuer); err == nil {
		t.Error("verify accepted a signature under the wrong issuer key")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	if err := Verify("server", "id", "!!notb64!!", "!!notb64!!"); err == nil {
		t.Error("verify accepted garbage base64")
	}
}
