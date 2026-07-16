package cryptobox

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	pt := []byte("the admin bundle contents")
	ct, err := Encrypt(pt, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ct, pt) {
		t.Fatal("ciphertext contains plaintext — not encrypted")
	}
	got, err := Decrypt(ct, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, pt)
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	ct, err := Encrypt([]byte("secret"), "right")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(ct, "wrong"); err == nil {
		t.Fatal("expected error for wrong passphrase")
	}
}

func TestDecryptTampered(t *testing.T) {
	ct, err := Encrypt([]byte("secret"), "pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct[len(ct)-1] ^= 0xff
	if _, err := Decrypt(ct, "pw"); err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestDecryptShortAndBadMagic(t *testing.T) {
	if _, err := Decrypt([]byte("xx"), "pw"); err == nil {
		t.Fatal("expected error for too-short input")
	}
	if _, err := Decrypt([]byte("NOTBOX................................"), "pw"); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

// An empty passphrase is allowed (used for user-context bundles): it seals and
// opens with "", and a non-empty passphrase must NOT open it.
func TestEncryptEmptyPassphrase(t *testing.T) {
	sealed, err := Encrypt([]byte("hello"), "")
	if err != nil {
		t.Fatalf("Encrypt with empty passphrase: %v", err)
	}
	plain, err := Decrypt(sealed, "")
	if err != nil {
		t.Fatalf("Decrypt with empty passphrase: %v", err)
	}
	if string(plain) != "hello" {
		t.Fatalf("round-trip = %q, want hello", plain)
	}
	if _, err := Decrypt(sealed, "wrong"); err == nil {
		t.Fatal("expected decrypt failure with a non-empty passphrase")
	}
}
