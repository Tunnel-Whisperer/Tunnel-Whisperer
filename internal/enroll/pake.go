// Package enroll implements zero-file enrollment: one-time invite codes are
// SPAKE2 passwords from which both sides derive a mutually authenticated
// AEAD channel, tunnelled through the relay as ciphertext only.
package enroll

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
	"salsa.debian.org/vasudev/gospake2"
)

// Side selects the SPAKE2 role: the enrollee is side A, the issuer side B.
type Side int

const (
	Enrollee Side = iota
	Issuer
)

// Session is one PAKE handshake: Start emits our message, Finish consumes the
// peer's and derives the AEAD key. Every box and the SAS derive from that key.
type Session struct {
	spake gospake2.SPAKE2
	key   []byte // 32-byte AEAD key, set by Finish
}

func NewSession(side Side, code, issuerTok string) *Session {
	pw := gospake2.NewPassword(code)
	idA := gospake2.NewIdentityA("tw-enrollee")
	idB := gospake2.NewIdentityB("tw-issuer/" + issuerTok)
	s := &Session{}
	if side == Enrollee {
		s.spake = gospake2.SPAKE2A(pw, idA, idB)
	} else {
		s.spake = gospake2.SPAKE2B(pw, idA, idB)
	}
	return s
}

func (s *Session) Start() []byte { return s.spake.Start() }

func (s *Session) Finish(peerMsg []byte) error {
	k, err := s.spake.Finish(peerMsg)
	if err != nil {
		return fmt.Errorf("pake finish: %w", err)
	}
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, k, nil, []byte("tw-enroll v1 aead")), key); err != nil {
		return fmt.Errorf("deriving aead key: %w", err)
	}
	s.key = key
	return nil
}

// Seal encrypts plaintext into a box: XChaCha20-Poly1305 with a random
// 24-byte nonce prepended to the ciphertext.
func (s *Session) Seal(plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(s.key)
	if err != nil {
		return nil, fmt.Errorf("creating aead: %w", err)
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return append(nonce, aead.Seal(nil, nonce, plaintext, nil)...), nil
}

func (s *Session) Open(box []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(s.key)
	if err != nil {
		return nil, fmt.Errorf("creating aead: %w", err)
	}
	if len(box) < chacha20poly1305.NonceSizeX {
		return nil, fmt.Errorf("box too short")
	}
	pt, err := aead.Open(nil, box[:chacha20poly1305.NonceSizeX], box[chacha20poly1305.NonceSizeX:], nil)
	if err != nil {
		return nil, fmt.Errorf("opening box: %w", err)
	}
	return pt, nil
}

// SAS derives the short authentication string, bound to the session key AND
// the enrollee's offer plaintext (which carries its public keys): a MITM who
// substituted keys cannot display the same string. Format "XXX-XXX", base32.
func (s *Session) SAS(offerPlaintext []byte) string {
	salt := sha256.Sum256(offerPlaintext)
	out := make([]byte, 4)
	if _, err := io.ReadFull(hkdf.New(sha256.New, s.key, salt[:], []byte("tw-sas v1")), out); err != nil {
		return "" // unreachable with sha256; keep the signature simple
	}
	code := base32.StdEncoding.EncodeToString(out)[:6]
	return code[:3] + "-" + code[3:]
}
