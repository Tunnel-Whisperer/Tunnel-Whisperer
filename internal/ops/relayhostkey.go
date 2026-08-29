package ops

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// The relay is provisioned to present a tw-generated SSH host key (injected via
// cloud-init / the install script), so the admin can PIN it on the direct
// port-22 management channel (DirectRelaySSH) instead of accepting any key.
// Without this, an on-path attacker can impersonate the relay's sshd on the
// public port 22 and receive the admin's hardening commands (finding SP-7).
//
// The private half never leaves the operator's machine except inside the
// provisioning payload, which already carries the tunnel's VLESS UUID — so this
// does not widen the trust boundary of that payload.

func relayHostKeyPrivPath() string { return filepath.Join(config.Dir(), "relay_host_ed25519") }
func relayHostKeyPubPath() string  { return filepath.Join(config.Dir(), "relay_host_ed25519.pub") }

// ensureRelayHostKey returns the relay's SSH host keypair (OpenSSH private-key
// PEM + the authorized-key public line), generating and persisting it on first
// use and reusing it on every later (re)provision so the pin stays stable.
func ensureRelayHostKey() (privPEM []byte, pubLine string, err error) {
	privPath, pubPath := relayHostKeyPrivPath(), relayHostKeyPubPath()
	if priv, rerr := os.ReadFile(privPath); rerr == nil {
		if pub, perr := os.ReadFile(pubPath); perr == nil {
			return priv, strings.TrimSpace(string(pub)), nil
		}
	}
	priv, pub, err := twssh.GenerateKeyPair()
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		return nil, "", err
	}
	return priv, strings.TrimSpace(string(pub)), nil
}

// loadPinnedRelayHostKey returns the pinned relay host public key, or nil when
// none has been recorded — i.e. a relay provisioned before host-key pinning, in
// which case DirectRelaySSH falls back to trust-on-connect with a warning.
func loadPinnedRelayHostKey() (gossh.PublicKey, error) {
	data, err := os.ReadFile(relayHostKeyPubPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pub, _, _, _, err := gossh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, err
	}
	return pub, nil
}
