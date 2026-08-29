package ops

import (
	"bytes"
	"fmt"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

// canonicalAuthorizedKey validates an externally-supplied SSH public key and
// returns its canonical, single-line authorized_keys form ("<type> <base64>").
//
// This is the single chokepoint that keeps attacker-controlled key material
// from breaking out of the line (and the per-key restrictions) it is written
// on. strings.TrimSpace — which every call site used to rely on — only strips
// SURROUNDING whitespace, so a key like "valid\nunrestricted-key" would inject
// a second, option-free authorized_keys line. gossh.ParseAuthorizedKey parses
// only the first line and returns the remainder in `rest`, which callers were
// discarding; we reject any non-empty remainder here.
//
// Re-marshalling the parsed key (rather than echoing the input) also drops any
// comment and any embedded options, so a value such as
// `command="…" ssh-ed25519 AAAA…` cannot smuggle an option onto the rendered
// line either. Only the key type and its base64 body survive.
func canonicalAuthorizedKey(raw string) (string, error) {
	pub, _, _, rest, err := gossh.ParseAuthorizedKey([]byte(raw))
	if err != nil {
		return "", fmt.Errorf("invalid ssh public key: %w", err)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return "", fmt.Errorf("ssh public key must be exactly one key on one line (trailing data rejected)")
	}
	return strings.TrimSpace(string(gossh.MarshalAuthorizedKey(pub))), nil
}
