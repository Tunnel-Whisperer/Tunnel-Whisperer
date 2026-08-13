//go:build e2e

package e2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testServerJoin drives the real invite-based server enrollment: the admin
// mints a one-time invite code (`tw relay invite`), the server redeems it
// (`tw join`) — reading back a shared authentication string (SAS) that both
// sides must agree on before the admin approves — and the result lands as a
// mode-signed context, born already active on the freshly-wiped server
// container. It then starts the echo target and the server daemon and
// proves the tunnel is up via `tw server test`.
func testServerJoin(t *testing.T) {
	scenario(t, "a server joins the admin's relay non-disruptively and publishes its reverse tunnel",
		"tw relay invite mints a one-time code and waits for the enrollee",
		"tw join <relay-host> <code> shows the SAS, gets approved on an exact read-back match, and creates a mode-signed server context",
		"the invite's EnrollServer step re-renders + reloads the relay Caddyfile ('Caddyfile reloaded') AFTER SAS approval",
		"the fresh server container has no live profile, so tw join activates the new context immediately (no separate use-context)",
		"tw server start + echo target come up and tw server test reports 'tunnel and shell working'",
		"the local_certs shim reapply lands inside EnrollServer's ~15s SSH-dial retry budget with margin to spare")

	// The server container's /etc/tw-test may carry state from an earlier full
	// suite run (this suite must be re-runnable); wipe it so `tw join`
	// always starts from a clean identity (and so liveProfileEmpty() is true,
	// meaning tw join auto-activates the new context), same rationale as
	// RelayInstall's admin seed wipe. A prior run's detached `tw server
	// start`/`echo-server` processes also outlive the container across test
	// invocations (nothing ever stops them) and keep holding the relay-side
	// reverse-forward port — since the admin registry restarts allocation
	// from the same first port after every fresh RelayInstall wipe, a
	// leftover process from an earlier run collides with this run's server
	// for that exact port ("tcpip-forward request denied by peer"). Kill any
	// survivors first (skip our own PID — this script's own
	// /proc/self/cmdline literally contains the search text, so it would
	// otherwise match itself).
	t.Log("killing any leftover tw server/echo-server processes and wiping server config dir for a clean identity before join")
	killMatching(t, "server", "tw server start")
	killMatching(t, "server", "echo-server")
	execIn(t, "server", "rm -rf /etc/tw-test")

	// The whole exchange: admin mints the code, server redeems it. See
	// harness.go's runInviteExchange for the FIFO/SAS/Caddyfile-reload
	// mechanics — including why the shim reapply happens AFTER SAS approval
	// now (EnrollServer runs only once the human confirms the read-back),
	// unlike the old file-based flow where the reload could happen before
	// any interactive step.
	issuerLog, joinLog := runInviteExchange(t, "admin", "tw relay invite", "server", "tw join "+domain+" {code}")

	if strings.Contains(issuerLog, "mode is unsigned") || strings.Contains(joinLog, "mode is unsigned") {
		fatalf(t, "invite/join printed the unsigned-mode warning:\nissuer:\n%s\njoin:\n%s", issuerLog, joinLog)
	}
	if !regexp.MustCompile(`Context "relay-tw-test" \(server\) created\.`).MatchString(joinLog) {
		fatalf(t, "join log missing the expected context-created line:\n%s", joinLog)
	}
	// The server's /etc/tw-test was wiped above, so liveProfileEmpty() was
	// true and tw join activated the context itself — confirm the "Next: tw
	// server start" hint (not "Next: tw config use-context ..."), proving no
	// manual switch is needed.
	if !strings.Contains(joinLog, "Next: tw server start") {
		fatalf(t, "join did not report auto-activation (expected 'Next: tw server start'):\n%s", joinLog)
	}

	// Echo target + server daemon.
	execDetached(t, "server", "echo-server -port "+echoPort)
	execDetached(t, "server", "tw server start > /var/log/tw-server.log 2>&1")

	waitFor(t, "server tunnel up", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})
	out := execIn(t, "server", "tw server status")
	t.Logf("server status:\n%s", out)

	// Tamper-evidence: flip mode: server -> mode: relay in the active config
	// (TW_CONFIG_DIR=/etc/tw-test in the e2e images, see e2e/images/tw/Dockerfile,
	// so the active file is /etc/tw-test/config.yaml — config.FilePath()).
	// A relay-gated command must now be refused by the mode-signature check,
	// not merely by the plain role gate, since the signed mode no longer
	// matches the profile's identity.
	execIn(t, "server", `sed -i 's/^mode: server/mode: relay/' /etc/tw-test/config.yaml`)
	if tamperOut, tamperErr := execInOK("server", "tw relay get-servers"); tamperErr == nil {
		fatalf(t, "relay command succeeded on a tampered server profile:\n%s", tamperOut)
	} else if !strings.Contains(tamperOut, "mode signature invalid") {
		fatalf(t, "expected a mode-signature error after tampering mode: server -> relay, got:\n%s", tamperOut)
	}
	// Restore so later scenarios (PermitOpen, Revocation, SecondTenant) are unaffected.
	execIn(t, "server", `sed -i 's/^mode: relay/mode: server/' /etc/tw-test/config.yaml`)
	t.Logf("server config restored after tamper test; status:\n%s", execIn(t, "server", "tw server status"))
}

// testSecondTenant enrolls a SECOND server (the relay's third tenant, after
// the admin and server-1), proves the enrollment is live and non-disruptive,
// then UN-enrolls it while its tunnel is live and proves the removal is
// total (registry row gone, relay listener killed, fresh tunnel test fails)
// and equally non-disruptive to the remaining tenants. Tenant ISOLATION
// (server A's client cannot reach server B) is still deferred.
func testSecondTenant(t *testing.T) {
	scenario(t, "a second server enrolls on the same relay (third tenant) non-disruptively",
		"tw relay invite on the admin mints a code for the new tenant; server2 redeems it with tw join",
		"the invite's EnrollServer live-adds the tenant (Caddyfile reloaded, no xray restart); tw relay get-servers lists both tenants",
		"server2's context is born active (fresh container, no live profile) and tw server test reports 'tunnel and shell working'",
		"server-1's tw server test and the admin's tw relay test still pass (non-disruptive)",
		"tw relay un-enroll-server --yes removes the LIVE server2: registry row gone, relay listener gone, its tunnel test fails",
		"server-1 and the admin remain unaffected after the un-enroll (non-disruptive removal)",
		"tab completion: tw __complete relay un-enroll-server offers the enrolled server-id")

	// Clean identity on server2 (same rationale as ServerJoin's wipe — this
	// also makes liveProfileEmpty() true so tw join auto-activates below).
	killMatching(t, "server2", "tw server start")
	execIn(t, "server2", "rm -rf /etc/tw-test")

	// The ServerID is prefixed with the container's hostname — a random
	// Docker ID here, NOT the compose service name — capture it to build
	// get-servers row regexes below.
	host := strings.TrimSpace(execIn(t, "server2", "hostname"))

	issuerLog, joinLog := runInviteExchange(t, "admin", "tw relay invite", "server2", "tw join "+domain+" {code}")
	if strings.Contains(issuerLog, "mode is unsigned") || strings.Contains(joinLog, "mode is unsigned") {
		fatalf(t, "invite/join printed the unsigned-mode warning:\nissuer:\n%s\njoin:\n%s", issuerLog, joinLog)
	}
	if !regexp.MustCompile(`Context "relay-tw-test" \(server\) created\.`).MatchString(joinLog) {
		fatalf(t, "join log missing the expected context-created line:\n%s", joinLog)
	}

	// get-servers queries the relay live: both tenants are registered with
	// their /tw/ paths. server-1's reverse tunnel is up (its daemon runs
	// since ServerJoin); server2's tunnel state isn't asserted here — like
	// the old file-based flow, `tw server test` below is self-sufficient
	// (internal/cli/test_relay.go: falls back to a standalone dial when no
	// `tw server start` daemon is running to talk to over gRPC), so this
	// registry row is only used to capture server2's ID/port for the
	// un-enroll steps below, not to prove liveness.
	serverHost := strings.TrimSpace(execIn(t, "server", "hostname"))
	regOut := execIn(t, "admin", "tw relay get-servers")
	if !regexp.MustCompile(`(?m)^` + serverHost + `\S*\s+/tw/` + serverHost + `\S*\s+\d+\s+\S+\s+up\s*$`).MatchString(regOut) {
		fatalf(t, "get-servers does not show server-1 (%s-*) with its path and TUNNEL up:\n%s", serverHost, regOut)
	}
	row := regexp.MustCompile(`(?m)^(` + host + `\S*)\s+/tw/` + host + `\S*\s+(\d+)\s+\S+\s+\S+\s*$`).FindStringSubmatch(regOut)
	if row == nil {
		fatalf(t, "get-servers does not show server2 (%s-*) with its path:\n%s", host, regOut)
	}
	server2ID, server2Port := row[1], row[2]

	// Tab completion offers the enrolled server-id for un-enroll-server.
	compOut := execIn(t, "admin", `tw __complete relay un-enroll-server ""`)
	if !strings.Contains(compOut, server2ID) {
		fatalf(t, "un-enroll-server completion does not offer %s:\n%s", server2ID, compOut)
	}

	// server2's own tunnel works — this is the exact path reported broken in
	// the field for a third tenant (VLESS dials, SSH never lands).
	waitFor(t, "server2 tunnel up", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server2", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})

	// Non-disruptive: the existing tenants still work.
	out := execIn(t, "server", "tw server test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "server-1 tunnel broken after server2 enroll:\n%s", out)
	}
	out = execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin tunnel broken after server2 enroll:\n%s", out)
	}

	// 6. Un-enroll server2 while its daemon runs and its tunnel is LIVE —
	// removal must be total: config gone AND live connections killed. The
	// --yes flag skips the confirmation prompt (no TTY here).
	out = execIn(t, "admin", "tw relay un-enroll-server "+server2ID+" --yes")
	if !strings.Contains(out, "Un-enrolled "+server2ID) {
		fatalf(t, "un-enroll did not report success:\n%s", out)
	}
	// The un-enroll re-rendered the Caddyfile, wiping the local_certs shim —
	// reapply before anything opens a fresh TLS connection to the relay.
	localCertsShim(t)

	// The reverse-tunnel listener is gone: no LISTEN (state 0A) row on
	// server2's port in the relay's /proc/net/tcp{,6}. This proves the kill,
	// not just the config removal — the sshd session would survive the
	// authorized_keys rewrite alone.
	portN, err := strconv.Atoi(server2Port)
	if err != nil {
		fatalf(t, "unparseable PORT column %q", server2Port)
	}
	proc := execIn(t, "relay", "cat /proc/net/tcp /proc/net/tcp6")
	if regexp.MustCompile(fmt.Sprintf(`(?m)^\s*\d+: [0-9A-F]+:%04X\s+\S+\s+0A\b`, portN)).MatchString(proc) {
		fatalf(t, "relay still LISTENs on server2's port %s after un-enroll:\n%s", server2Port, proc)
	}

	// The registry no longer lists it.
	regOut = execIn(t, "admin", "tw relay get-servers")
	if regexp.MustCompile(`(?m)^` + host).MatchString(regOut) {
		fatalf(t, "get-servers still lists server2 after un-enroll:\n%s", regOut)
	}

	// server2 is dark: a fresh tunnel test must FAIL (its inbound and its CA
	// trust are gone from the relay).
	if testOut, testErr := execInOK("server2", "tw server test"); testErr == nil && strings.Contains(testOut, "tunnel and shell working") {
		fatalf(t, "server2 tunnel still works after un-enroll:\n%s", testOut)
	}
	killMatching(t, "server2", "tw server start") // stop its reconnect spam

	// 7. Still non-disruptive: server-1 and the admin are unaffected.
	out = execIn(t, "server", "tw server test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "server-1 tunnel broken after server2 un-enroll:\n%s", out)
	}
	out = execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin tunnel broken after server2 un-enroll:\n%s", out)
	}
}

// mtlsForeignAlert is the stable substring of the OpenSSL/curl error text
// actually observed (live, --http1.1) for a foreign-CA client cert rejection
// — see /home/n/code/Tunnel-Whisperer/.claude/superpowers/sdd/task-6-report.md
// and the "Fix round 1" section appended there. It's deliberately the
// *specific* alert wording, not generic words like "certificate" or
// "handshake", which also show up in an unrelated server-trust failure
// (e.g. "unable to get local issuer certificate") and would let this test
// pass for the wrong reason.
const mtlsForeignAlert = "unknown ca"

// assertMTLSRejection fails the test unless out contains the expected
// client_auth-gate alert substring and does NOT contain any of the telltale
// substrings of a server-trust failure (client not trusting the relay's own
// server certificate) — a different, wrong reason for the connection to
// fail that the earlier looser assertion (`contains "certificate" or
// "handshake"`) could not tell apart from the real gate rejection.
func assertMTLSRejection(t *testing.T, label, out, wantAlert string) {
	t.Helper()
	if !strings.Contains(out, wantAlert) {
		fatalf(t, "%s: expected the client_auth gate's %q alert, got:\n%s", label, wantAlert, out)
	}
	for _, trustFailure := range []string{"unable to get local issuer", "self-signed certificate"} {
		if strings.Contains(out, trustFailure) {
			fatalf(t, "%s: output contains %q — this looks like a server-trust failure (client doesn't trust the relay's own cert), not the client_auth gate rejecting a bad/missing client cert:\n%s", label, trustFailure, out)
		}
	}
}

// testMTLSGate proves the relay's Caddy client_auth gate's current shape
// (mode verify_if_given, landed in 6f25359 to admit certless /enroll/
// traffic for the invite flow): a PRESENTED cert is still verified against
// the trust pool, but presenting none no longer aborts the TLS handshake —
// the request completes and is then refused by the per-route CN matcher
// instead, landing on the Caddyfile's catch-all 404.
func testMTLSGate(t *testing.T) {
	scenario(t, "the relay's Caddy client_auth gate (verify_if_given) validates presented certs; routing (not the handshake) gates unmapped paths",
		"an HTTPS request with NO client cert now completes the TLS handshake and gets HTTP 404 from the catch-all — not a TLS alert",
		"an HTTPS request with a FOREIGN self-signed cert is still rejected at the TLS layer with the 'unknown ca' alert (verify_if_given still verifies any PRESENTED cert)",
		"an HTTPS request with a VALID cert (server-1's own, admitted during ServerJoin) on a path that isn't its own /tw/<id>* prefix also gets 404 — cert validity alone never grants access to an unmapped path",
		"the foreign-CA rejection is not a server-trust failure (the client does trust the relay's own server cert) — proving the gate, not a misconfig, is what refuses it")

	// No client cert: the TLS handshake now SUCCEEDS under verify_if_given
	// (changed from require_and_verify — see internal/relay/caddy/Caddyfile.tmpl
	// and render_test.go). The request then falls through every per-tenant
	// @{{.ID}} matcher (the CN expression can't match an empty subject) and
	// every @enroll_{{.ID}} matcher (path doesn't match /enroll/<token>/*),
	// landing on the catch-all `handle { respond 404 }`.
	statusOut, err := execInOK("client", "curl -sS --max-time 10 -o /dev/null -w '%{http_code}' https://"+domain+"/")
	if err != nil {
		fatalf(t, "HTTPS without a client cert failed at the transport/TLS level (expected the handshake to succeed under verify_if_given):\n%s", statusOut)
	}
	if strings.TrimSpace(statusOut) != "404" {
		fatalf(t, "HTTPS without a client cert: expected HTTP 404 from the catch-all, got %q", statusOut)
	}

	// Foreign CA: a self-signed cert that IS presented must still be
	// rejected at the TLS layer — verify_if_given only waives the
	// requirement to present a cert, it still verifies any cert that is.
	//
	// --http1.1: over the default h2 ALPN, curl defers sending the request
	// until after the (successful, TLS1.3) handshake, so the server's
	// alert arrives mid-stream and curl only reports a generic
	// "getpeername() failed / Transport endpoint is not connected" /
	// "Broken pipe" — no "certificate" or "handshake" substring, even though
	// the rejection is real (confirmed directly with openssl s_client:
	// "tlsv13 alert unknown ca"). Forcing HTTP/1.1 makes curl surface the
	// OpenSSL alert text directly instead.
	execIn(t, "client", `cd /tmp && openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 `+
		`-keyout fake.key -out fake.crt -days 1 -nodes -subj /CN=intruder 2>/dev/null`)
	out, err := execInOK("client",
		"curl -sS --max-time 10 --http1.1 --cert /tmp/fake.crt --key /tmp/fake.key https://"+domain+"/ 2>&1")
	if err == nil {
		fatalf(t, "HTTPS with a foreign-CA cert unexpectedly succeeded:\n%s", out)
	}
	assertMTLSRejection(t, "foreign-CA cert", out, mtlsForeignAlert)

	// Valid cert (server-1's own, admitted during ServerJoin, run right
	// before this scenario), wrong path: TLS succeeds (its self-generated CA
	// was uploaded during EnrollServer and is in the relay's trust pool) but
	// the request doesn't match its own /tw/<server-id>* route, so it still
	// 404s from the catch-all.
	statusOut, err = execInOK("server", "curl -sS --max-time 10 -o /dev/null -w '%{http_code}' "+
		"--cert /etc/tw-test/client.crt --key /etc/tw-test/client.key https://"+domain+"/nonexistent-path")
	if err != nil {
		fatalf(t, "HTTPS with a valid client cert on an unmapped path failed at the transport/TLS level:\n%s", statusOut)
	}
	if strings.TrimSpace(statusOut) != "404" {
		fatalf(t, "HTTPS with a valid client cert on an unmapped path: expected HTTP 404, got %q", statusOut)
	}
}
