//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
)

// testAPIAuth is the SP-9 regression over the real daemon: the gRPC control
// plane rejects calls that do not present the daemon's bearer token. The token
// lives in a 0600 file the CLI reads; swapping it for a bogus value makes the
// CLI attach the wrong token while the daemon still expects the real one loaded
// at startup, so the RPC is rejected.
func testAPIAuth(t *testing.T) {
	scenario(t, "the gRPC control API requires the daemon's bearer token — loopback alone is not enough",
		"tw server user list works with the real on-disk token",
		"replacing api.token with a bogus value makes the same call fail with an auth error (the RPC is rejected, not served)",
		"restoring the token restores access")

	if _, err := execInOK("server", "tw server user list"); err != nil {
		t.Skip("server daemon/API not available for the API-auth probe")
	}

	// Swap in a bogus token, capturing the real one first. Restore BEFORE any
	// assertion so a failure never leaves the container without its token.
	execIn(t, "server", "cp /etc/tw-test/api.token /tmp/api.token.bak && printf 'bogus-not-the-real-token\\n' > /etc/tw-test/api.token")
	out, err := execInOK("server", "tw server user list 2>&1")
	execIn(t, "server", "cp /tmp/api.token.bak /etc/tw-test/api.token")

	if err == nil {
		fatalf(t, "the API call succeeded with a bogus token — the control plane is not enforcing auth:\n%s", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "unauthenticated") && !strings.Contains(low, "token") {
		fatalf(t, "expected an authentication error from the API, got:\n%s", out)
	}
	// Restore worked.
	if _, err := execInOK("server", "tw server user list"); err != nil {
		fatalf(t, "restoring api.token did not restore API access: %v", err)
	}
}

// testCrossTenantCert is the #10 regression over the real relay: on a shared
// trust pool, a tenant must not reach another tenant's route by minting a cert
// with the victim's CN off its own (pool-trusted) CA. The deployed Caddyfile
// pins the issuing CA per route, and a forged cross-tenant cert is refused.
//
// Runs after SecondTenant, so server-1 (attacker CA) and server-2 (victim) are
// both enrolled.
func testCrossTenantCert(t *testing.T) {
	scenario(t, "the relay binds each tunnel route to its tenant's issuing CA, not just the CN (shared-trust-pool isolation)",
		"the live relay Caddyfile matches the client-cert issuer (CN==server-id) on every tenant route",
		"a cert carrying the victim's CN but signed by the ATTACKER's own pool-trusted CA is refused at the victim's route (HTTP 404, not proxied to the victim's upstream)")

	// 1. The deployed relay config actually enforces the issuer check.
	caddyfile := execIn(t, "relay", "cat /etc/caddy/Caddyfile")
	if !strings.Contains(caddyfile, "http.request.tls.client.issuer") {
		fatalf(t, "the live relay Caddyfile does not pin the client-cert issuer — cross-tenant impersonation is possible:\n%s", caddyfile)
	}

	// 2. Forge a cert with the victim's CN off the attacker's CA and present it.
	// Victim = server-2's server-id, read from its own client cert subject.
	subj := execIn(t, "server2", "openssl x509 -in /etc/tw-test/client.crt -noout -subject 2>/dev/null")
	m := regexp.MustCompile(`CN\s*=\s*([A-Za-z0-9._-]+)`).FindStringSubmatch(subj)
	if m == nil {
		t.Skipf("could not read server-2's server-id from its client cert subject: %q", subj)
	}
	victimID := m[1]

	// On the attacker (server-1): issue a client cert CN=<victim-id> signed by
	// server-1's OWN CA (which is in the relay's union trust pool).
	forge := "cd /tmp && " +
		"openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -keyout forge.key -out forge.csr -nodes -subj /CN=" + victimID + " 2>/dev/null && " +
		"openssl x509 -req -in forge.csr -CA /etc/tw-test/ca.crt -CAkey /etc/tw-test/ca.key -CAcreateserial -days 1 -out forge.crt 2>/dev/null"
	execIn(t, "server", forge)

	code := strings.TrimSpace(execIn(t, "server",
		"curl -sS --max-time 10 -o /dev/null -w '%{http_code}' "+
			"--cert /tmp/forge.crt --key /tmp/forge.key https://"+domain+"/tw/"+victimID+"/"))
	if code != "404" {
		fatalf(t, "a forged cross-tenant cert (victim CN off the attacker's CA) reached the victim's route (HTTP %s, expected 404) — issuer binding is not enforced", code)
	}
}
