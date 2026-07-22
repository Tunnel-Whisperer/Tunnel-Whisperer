// Package modeauth signs and verifies a profile's operating mode. The
// signature is TAMPER-EVIDENCE, not a security boundary: it makes tw refuse a
// role's commands when the config `mode` field was hand-edited, failing fast
// with a clear message. It does NOT stop a user who fully controls their
// machine (they can regenerate the whole profile) — that is inherently
// unpreventable client-side and gains no real power, because the real role
// boundary is the relay's authorized_keys (restrict vs shell) and the mTLS/PKI
// trust chain, which key possession — not this field — decides.
package modeauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"

	gossh "golang.org/x/crypto/ssh"
)

const payloadPrefix = "tw-mode-v1"

// Payload is the canonical signed message binding a mode to a profile
// identity (the profile's own id_ed25519.pub, trimmed). Fields are
// length-prefixed so the encoding is injective — no (mode, identity) pair
// can collide with another via an embedded newline.
func Payload(mode, identity string) []byte {
	return []byte(fmt.Sprintf("%s\n%d:%s\n%d:%s", payloadPrefix, len(mode), mode, len(identity), identity))
}

// Sign signs Payload(mode, identity) with an OpenSSH ed25519 private key,
// returning base64 signature bytes and the base64 raw ed25519 public key.
func Sign(privPEM []byte, mode, identity string) (sigB64, issuerB64 string, err error) {
	signer, err := gossh.ParsePrivateKey(privPEM)
	if err != nil {
		return "", "", fmt.Errorf("parsing signer key: %w", err)
	}
	cpk, ok := signer.PublicKey().(gossh.CryptoPublicKey)
	if !ok {
		return "", "", errors.New("signer key is not an ed25519 key")
	}
	edPub, ok := cpk.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return "", "", errors.New("signer key is not ed25519")
	}
	sig, err := signer.Sign(nil, Payload(mode, identity))
	if err != nil {
		return "", "", fmt.Errorf("signing: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig.Blob),
		base64.StdEncoding.EncodeToString(edPub), nil
}

// Verify reports whether sigB64 is a valid signature over Payload(mode,
// identity) under the ed25519 public key issuerB64.
func Verify(mode, identity, sigB64, issuerB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("decoding signature: %w", err)
	}
	issuer, err := base64.StdEncoding.DecodeString(issuerB64)
	if err != nil {
		return fmt.Errorf("decoding issuer key: %w", err)
	}
	if len(issuer) != ed25519.PublicKeySize {
		return errors.New("issuer key is not a valid ed25519 public key")
	}
	if !ed25519.Verify(ed25519.PublicKey(issuer), Payload(mode, identity), sig) {
		return errors.New("mode signature does not verify")
	}
	return nil
}
