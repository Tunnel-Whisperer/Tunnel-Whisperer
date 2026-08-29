package ops

import (
	"fmt"
	"strings"

	"regexp"
)

// These validators guard operator/attacker-supplied identifiers before they
// are substituted into Terraform HCL (relay name) or a root-run bash install
// script (domain, ssh user). The templates render with text/template, which
// does NOT escape for those contexts, so a value containing a quote, `$()`,
// `;`, or a newline could break out and inject HCL or shell. Validating against
// a strict allowlist at every entry point is the root-cause fix (finding #3 and
// #6); the injection is impossible if the value can only be [a-z0-9-] etc.
var (
	// Cloud instance display name: 1-32 chars, lowercase alnum + hyphen, no
	// leading/trailing hyphen. Rendered as `tw-{{.Name}}` into .tf files.
	relayNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	// POSIX-ish user name, also safe as a `/etc/sudoers.d/99-<user>` filename.
	sshUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	// A single DNS label (RFC 1123): letters, digits, hyphen; no leading/
	// trailing hyphen; 1-63 chars.
	hostLabelRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

func validateRelayName(name string) error {
	if !relayNameRe.MatchString(name) {
		return fmt.Errorf("invalid relay name %q: use 1-32 chars of lowercase letters, digits, and hyphens (no leading/trailing hyphen)", name)
	}
	return nil
}

func validateSSHUser(user string) error {
	if !sshUserRe.MatchString(user) {
		return fmt.Errorf("invalid relay ssh user %q: use 1-32 chars matching [a-z_][a-z0-9_-]*", user)
	}
	return nil
}

// validateHostname accepts a DNS hostname/FQDN (each dot-separated label an
// RFC 1123 label). It deliberately rejects anything with shell/HCL-significant
// characters, so a validated hostname is safe to render into the install
// script and the Caddyfile.
func validateHostname(host string) error {
	if len(host) == 0 || len(host) > 253 {
		return fmt.Errorf("invalid relay domain %q: length must be 1-253 characters", host)
	}
	for _, label := range strings.Split(host, ".") {
		if !hostLabelRe.MatchString(label) {
			return fmt.Errorf("invalid relay domain %q: %q is not a valid DNS label", host, label)
		}
	}
	return nil
}
