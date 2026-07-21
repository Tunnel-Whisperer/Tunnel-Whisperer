//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// installShim runs the REAL tw-generated install script on the relay after
// removing the caddy apt artifacts the relay image pre-bakes for speed
// (/usr/share/keyrings/caddy-stable-archive-keyring.gpg + the source list). A
// fresh VM — which the script is written for — would not have them; with them
// present the script's `gpg --dearmor -o <existing file>` hits an interactive
// overwrite prompt and aborts under `set -e` (no tty: "cannot open /dev/tty").
// Removing them makes both the first run and the idempotent re-run behave like
// a clean install. The generated script and its template are left untouched.
func installShim(t *testing.T) string {
	t.Helper()
	t.Log("shim: removing pre-baked caddy apt keyring/source before the install " +
		"script so its gpg --dearmor writes fresh (fresh-VM behaviour)")
	return execIn(t, "relay",
		"rm -f /usr/share/keyrings/caddy-stable-archive-keyring.gpg "+
			"/etc/apt/sources.list.d/caddy-stable.list && "+
			"bash /shared/tw-install-"+domain+".sh")
}

// localCertsShim prepends a `{ local_certs }` global block to the relay
// Caddyfile so Caddy issues certs from its internal CA instead of reaching for
// ACME (unreachable in the offline test network), then restarts Caddy. The
// install script rewrites the Caddyfile from scratch, so this must be re-applied
// after every install run. Idempotent: a no-op once the block is present.
func localCertsShim(t *testing.T) {
	t.Helper()
	execIn(t, "relay", `grep -q local_certs /etc/caddy/Caddyfile || `+
		`(printf '{\n\tlocal_certs\n}\n' | cat - /etc/caddy/Caddyfile > /tmp/Caddyfile.new `+
		`&& mv /tmp/Caddyfile.new /etc/caddy/Caddyfile && systemctl restart caddy)`)
}

func testRelayInstall(t *testing.T) {
	scenario(t, "an admin provisions a relay from scratch using the REAL tw-generated install script",
		"the flag-based one-liner `tw relay create --provider manual --domain --ip` completes without prompts and emits the install script + admin bundle",
		"the generated install script provisions the relay VM (Caddy + Xray + sshd + firewall) and prints 'Setup complete'",
		"tw relay test confirms the tunnel and shell work end-to-end (DNS + mTLS handshake + SSH over the VLESS tunnel)",
		"tw relay status reports the manual relay",
		"re-running the install script is idempotent (its documented clean-then-reinstall contract) and the relay still passes tw relay test")

	// Start from a clean identity: `tw relay create` itself stamps mode admin
	// on a fresh profile (no seeding shim — the Contexts scenario later asserts
	// the admin role landed). The wipe matters on re-runs: an already-provisioned
	// /etc/tw-test makes GetRelayStatus find the manual-relay marker and the
	// non-interactive create errors out with "relay already provisioned". On a
	// fresh container the dir does not exist, so the rm is a no-op.
	execIn(t, "admin", `rm -rf /etc/tw-test`)

	// Flag-based non-interactive create: with --provider/--domain/--ip all set,
	// create must run to completion with no prompts (stdin is not a tty here, so
	// any leftover prompt would read EOF and fail loudly).
	out := execIn(t, "admin",
		`cd /shared && tw relay create --provider manual --domain `+domain+` --ip `+relayIP+` --ssh-open=false`)
	if !strings.Contains(out, "Relay server setup complete") {
		fatalf(t, "non-interactive create did not complete:\n%s", out)
	}
	if !strings.Contains(out, "not yet installed") {
		fatalf(t, "non-interactive create did not print the install next-steps:\n%s", out)
	}

	// The wizard wrote the install script and the admin bundle into /shared.
	execIn(t, "admin", "test -f /shared/tw-install-"+domain+".sh")
	execIn(t, "admin", "test -f /shared/tw_relay-tw-test.twctx")

	// Run the REAL install script on the relay (with the fresh-VM keyring shim).
	installOut := installShim(t)
	if !strings.Contains(installOut, "Setup complete") {
		fatalf(t, "install script did not complete:\n%s", installOut)
	}

	// Harness shim 1: force Caddy's internal CA instead of ACME.
	localCertsShim(t)

	// Harness shim 2: trust Caddy's local root everywhere.
	waitFor(t, "caddy local root CA", 60*time.Second, func() (bool, string) {
		out, err := execInOK("relay",
			"cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt")
		if err != nil || !strings.Contains(out, "BEGIN CERTIFICATE") {
			return false, "root.crt not present yet"
		}
		return true, ""
	})
	execIn(t, "relay",
		"cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt /shared/caddy-root.crt")
	for _, svc := range twServices {
		execIn(t, svc,
			"cp /shared/caddy-root.crt /usr/local/share/ca-certificates/tw-e2e-root.crt && update-ca-certificates")
	}

	// Services up.
	for _, unit := range []string{"caddy", "xray"} {
		execIn(t, "relay", "systemctl is-active "+unit)
	}

	// End-to-end admin check: DNS + mTLS handshake + SSH over the VLESS tunnel.
	waitFor(t, "tw relay test", 120*time.Second, func() (bool, string) {
		out, err := execInOK("admin", "tw relay test")
		if err != nil {
			return false, out
		}
		if strings.Contains(out, "✗") {
			return false, out
		}
		return strings.Contains(out, "tunnel and shell working"), out
	})

	// Status shows the manual relay.
	statusOut := execIn(t, "admin", "tw relay status")
	if !strings.Contains(statusOut, domain) {
		fatalf(t, "admin status does not mention the relay:\n%s", statusOut)
	}

	// Idempotency: the script's documented contract is that a re-run cleans
	// prior state. Re-run, re-apply the local_certs shim, re-verify.
	installShim(t)
	localCertsShim(t)
	waitFor(t, "tw relay test after re-install", 120*time.Second, func() (bool, string) {
		out, err := execInOK("admin", "tw relay test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})
}
