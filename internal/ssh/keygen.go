package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"

	gossh "golang.org/x/crypto/ssh"
)

// GenerateKeyPair creates a new ed25519 SSH key pair.
// Returns the private key in OpenSSH format and the public key in authorized_keys format.
func GenerateKeyPair() (privateKeyPEM []byte, publicKeyAuthorized []byte, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	pemBlock, err := gossh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return nil, nil, err
	}

	sshPub, err := gossh.NewPublicKey(pubKey)
	if err != nil {
		return nil, nil, err
	}

	return pem.EncodeToMemory(pemBlock), gossh.MarshalAuthorizedKey(sshPub), nil
}
