// Package cryptobox seals and opens byte blobs with a passphrase, using
// argon2id for key derivation and AES-256-GCM for authenticated encryption.
//
// NOTE ON CONFIDENTIALITY: tw deliberately ships passphrase-less — every caller
// seals with the empty passphrase. With an empty passphrase the argon2id key is
// derived from the (in-band) salt alone, so the AES-256-GCM layer provides
// framing and INTEGRITY but NOT confidentiality: a bundle can be opened by
// anyone who holds the file. Identity bundles (which carry the CA signing key,
// SSH host key, and client keys) are therefore protected only by 0600 file
// permissions and by being transferred over a trusted channel — never treat a
// .twctx blob as safe-at-rest encryption. Output layout:
//
//	magic("TWBOX1") | salt(16) | nonce(12) | AES-256-GCM ciphertext
package cryptobox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	magic        = "TWBOX1"
	saltLen      = 16
	nonceLen     = 12
	keyLen       = 32
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
)

// ErrDecrypt is returned when opening a bundle fails — a wrong passphrase or
// corrupted data. Callers use errors.Is to distinguish "needs a passphrase"
// from other failures (e.g. import trying an empty passphrase first).
var ErrDecrypt = errors.New("cryptobox: decryption failed (wrong passphrase or corrupted data)")

func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, keyLen)
}

// Encrypt seals plaintext under passphrase and returns the framed ciphertext.
func Encrypt(plaintext []byte, passphrase string) ([]byte, error) {
	// An empty passphrase is allowed: it produces a bundle openable with "" (no
	// real protection). Used for user-context bundles, which are shared over an
	// already-trusted channel and deliberately carry no passphrase. Callers that
	// require a passphrase (admin bundles) enforce it before calling here.
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("cryptobox: reading salt: %w", err)
	}
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("cryptobox: reading nonce: %w", err)
	}
	out := make([]byte, 0, len(magic)+saltLen+nonceLen+len(plaintext)+gcm.Overhead())
	out = append(out, []byte(magic)...)
	out = append(out, salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, nil), nil
}

// Decrypt opens framed ciphertext produced by Encrypt. Any authentication
// failure (wrong passphrase or corruption) returns a single generic error.
func Decrypt(data []byte, passphrase string) ([]byte, error) {
	hdr := len(magic) + saltLen + nonceLen
	if len(data) < hdr {
		return nil, errors.New("cryptobox: ciphertext too short")
	}
	if string(data[:len(magic)]) != magic {
		return nil, errors.New("cryptobox: bad magic (not a tw bundle)")
	}
	salt := data[len(magic) : len(magic)+saltLen]
	nonce := data[len(magic)+saltLen : hdr]
	ciphertext := data[hdr:]
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

func newGCM(passphrase string, salt []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		return nil, fmt.Errorf("cryptobox: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: gcm: %w", err)
	}
	return gcm, nil
}
