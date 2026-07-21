//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// testServerJoin drives the real server-join wizard: the server generates a
// join request (this also sets mode=server), the admin enrolls it over the
// VLESS tunnel to the relay, and the server applies the enrollment response.
// It then starts the echo target and the server daemon and proves the tunnel
// is up via `tw server test`.
func testServerJoin(t *testing.T) {
	scenario(t, "a server joins the admin's relay non-disruptively and publishes its reverse tunnel",
		"tw server join generates a join request (and sets mode=server)",
		"tw relay enroll-server registers the tenant over the tunnel and re-renders + reloads the relay Caddyfile ('Caddyfile reloaded')",
		"tw server join --apply applies the admin's enrollment response",
		"tw server start + echo target come up and tw server test reports 'tunnel and shell working'",
		"the local_certs shim reapply lands inside enroll's ~15s SSH-dial retry budget with >5s of margin")

	// The server container's /etc/tw-test may carry state from an earlier full
	// suite run (this suite must be re-runnable); wipe it so `tw server join`
	// always starts from a clean identity, same rationale as RelayInstall's
	// admin seed wipe. A prior run's detached `tw server start`/`echo-server`
	// processes also outlive the container across test invocations (nothing
	// ever stops them) and keep holding the relay-side reverse-forward port —
	// since the admin registry restarts allocation from the same first port
	// after every fresh RelayInstall wipe, a leftover process from an earlier
	// run collides with this run's server for that exact port ("tcpip-forward
	// request denied by peer"). Kill any survivors first (skip our own PID —
	// this script's own /proc/self/cmdline literally contains the search
	// text, so it would otherwise match itself).
	t.Log("killing any leftover tw server/echo-server processes and wiping server config dir for a clean identity before join")
	killMatching(t, "server", "tw server start")
	killMatching(t, "server", "echo-server")
	execIn(t, "server", "rm -rf /etc/tw-test")

	// 1. Server generates identity + join request (this also sets mode=server).
	execIn(t, "server", "cd /shared && rm -f tw_join_*.json && tw server join "+domain)

	// 2. Admin enrolls it (SSH to relay over the VLESS tunnel) and writes the
	// response. `tw relay enroll-server` step 3 ("Apply relay config") fully
	// re-renders and reloads the relay's Caddyfile from scratch (it rebuilds
	// the whole per-tenant handle-block set), which wipes the local_certs
	// shim RelayInstall applied. Caddy then falls back to real ACME for the
	// fake "relay.tw.test" domain, which fails (not a public suffix) and
	// leaves the site briefly without a servable certificate — the same
	// class of issue relay_install_test.go's idempotency pass guards
	// against, just triggered by a different relay-side command, and this
	// time from *inside* a single CLI invocation with no seam for the
	// harness to intervene between step 3's reload and step 4's fresh dial.
	//
	// Racing a fix into that seam (sub-20ms in practice) would be flaky. Ops
	// already retries step 4's SSH dial 15x over ~15s (internal/ops/user.go)
	// — real tolerance for exactly this kind of transient relay unavailability
	// (e.g. a cert reissue in flight). So: run enroll detached, wait for its
	// own step-3-complete log line (proving the reload already happened and
	// the pubkey/config substeps that reuse the pre-reload SSH connection are
	// done — reapplying the shim any earlier risks tearing down that
	// still-in-use connection out from under step 3), reapply the shim, then
	// let one of step 4's remaining retries land on a healthy relay.
	execDetached(t, "admin",
		"cd /shared && rm -f tw_join_response_*.json && tw relay enroll-server /shared/tw_join_*.json > /shared/enroll.log 2>&1")
	waitFor(t, "admin enroll step 3 (Apply relay config) complete", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "cat /shared/enroll.log 2>/dev/null")
		return strings.Contains(out, "Caddyfile reloaded"), out
	})
	// This reapply races the SSH-dial retry loop that enroll's step 4
	// (Live enroll tenant, RelaySSH) is already inside: internal/ops/user.go's
	// `for i := 0; i < 15; i++ { dial; sleep 1s }` — a fixed ~15s total
	// budget, no backoff. The harness's own confirmation waitFor below is
	// deliberately looser (60s) so it never itself times out a slow-but-live
	// relay, which means it would NOT notice if the shim reapply were slow
	// enough that step 4 exhausted its 15s and failed first. So: time the
	// reapply-to-confirmed-live window here and flag it loudly if it starts
	// eating into that 15s budget, rather than relying on the 60s waitFor
	// (which has no opinion on the product's much tighter constant).
	shimReapplyStart := time.Now()
	localCertsShim(t)
	waitFor(t, "caddy local root CA after enroll reload", 60*time.Second, func() (bool, string) {
		out, err := execInOK("relay",
			"cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt")
		if err != nil || !strings.Contains(out, "BEGIN CERTIFICATE") {
			return false, "root.crt not present yet"
		}
		return true, ""
	})
	shimReapplyElapsed := time.Since(shimReapplyStart)
	t.Logf("shim reapply confirmed live after %s (racing enroll's 15x1s SSH-dial retry budget in internal/ops/user.go)", shimReapplyElapsed)
	if shimReapplyElapsed > 10*time.Second {
		t.Errorf("shim reapply took %s — less than 5s of margin left against enroll step 4's ~15s SSH-dial retry budget (internal/ops/user.go); this is a near-miss, not (yet) an observed failure, but the margin this test depends on is shrinking and should be investigated before it flakes", shimReapplyElapsed)
	}
	waitFor(t, "admin enroll response file", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "ls /shared/tw_join_response_*.json 2>/dev/null")
		if strings.Contains(out, "tw_join_response_") {
			return true, ""
		}
		logOut, _ := execInOK("admin", "cat /shared/enroll.log 2>/dev/null")
		return false, logOut
	})

	// 3. Server applies the response.
	execIn(t, "server", "cd /shared && tw server join --apply /shared/tw_join_response_*.json")

	// 4. Echo target + server daemon.
	execDetached(t, "server", "echo-server -port "+echoPort)
	execDetached(t, "server", "tw server start > /var/log/tw-server.log 2>&1")

	waitFor(t, "server tunnel up", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})
	out := execIn(t, "server", "tw server status")
	t.Logf("server status:\n%s", out)
}

// testSecondTenant enrolls a SECOND server (the relay's third tenant, after
// the admin and server-1) and proves the enrollment is live and non-disruptive:
// server2 passes `tw server test`, and server-1 + admin still pass theirs.
// This mirrors the real-world multi-server flow; tenant ISOLATION (server A's
// client cannot reach server B) is still deferred to a later scenario.
func testSecondTenant(t *testing.T) {
	scenario(t, "a second server enrolls on the same relay (third tenant) non-disruptively",
		"tw server join on server2 generates its join request",
		"tw relay enroll-server live-adds the tenant (Caddyfile reloaded, no xray restart); tw relay get-servers lists both tenants",
		"tw server join --apply applies the response on server2",
		"server2's tw server test reports 'tunnel and shell working'",
		"server-1's tw server test and the admin's tw relay test still pass (non-disruptive)")

	// Clean identity on server2 (same rationale as ServerJoin's wipe).
	killMatching(t, "server2", "tw server start")
	execIn(t, "server2", "rm -rf /etc/tw-test")

	// The ServerID (and so the join/response filenames) is prefixed with the
	// container's hostname — a random Docker ID here, NOT the compose service
	// name — so capture it to address server2's files without glob-colliding
	// with server-1's leftovers in /shared.
	host := strings.TrimSpace(execIn(t, "server2", "hostname"))
	joinGlob := "/shared/tw_join_" + host + "-*.json"
	respGlob := "/shared/tw_join_response_" + host + "-*.json"

	// 1. server2 generates identity + join request.
	execIn(t, "server2", "cd /shared && rm -f "+joinGlob+" "+respGlob+" && tw server join "+domain)
	execIn(t, "server2", "ls "+joinGlob) // fail loudly here if the naming assumption breaks

	// 2. Admin enrolls it — same detached + shim-reapply dance as ServerJoin:
	// enroll's step 3 re-renders the relay Caddyfile, wiping the local_certs
	// shim, so it must be reapplied before enroll's step-4 SSH-dial retries
	// exhaust their ~15s budget.
	execDetached(t, "admin",
		"cd /shared && tw relay enroll-server "+joinGlob+" > /shared/enroll2.log 2>&1")
	waitFor(t, "admin enroll (server2) step 3 complete", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "cat /shared/enroll2.log 2>/dev/null")
		return strings.Contains(out, "Caddyfile reloaded"), out
	})
	localCertsShim(t)
	waitFor(t, "admin enroll (server2) response file", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "ls "+respGlob+" 2>/dev/null")
		if strings.Contains(out, "tw_join_response_"+host+"-") {
			return true, ""
		}
		logOut, _ := execInOK("admin", "cat /shared/enroll2.log 2>/dev/null")
		return false, logOut
	})

	// The admin registry lists both servers.
	serverHost := strings.TrimSpace(execIn(t, "server", "hostname"))
	regOut := execIn(t, "admin", "tw relay get-servers")
	if !strings.Contains(regOut, host+"-") || !strings.Contains(regOut, serverHost+"-") {
		fatalf(t, "admin servers does not list both tenants (%s-*, %s-*):\n%s", host, serverHost, regOut)
	}

	// 3. server2 applies the response.
	execIn(t, "server2", "cd /shared && tw server join --apply "+respGlob)

	// 4. The new tenant's own tunnel works — this is the exact path reported
	// broken in the field for a third tenant (VLESS dials, SSH never lands).
	waitFor(t, "server2 tunnel up", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server2", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})

	// 5. Non-disruptive: the existing tenants still work.
	out := execIn(t, "server", "tw server test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "server-1 tunnel broken after server2 enroll:\n%s", out)
	}
	out = execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin tunnel broken after server2 enroll:\n%s", out)
	}
}

// mtlsNoCertAlert and mtlsForeignCAAlert are the stable substrings of the
// OpenSSL/curl error text actually observed (live, --http1.1) for each
// rejection case — see /home/n/code/Tunnel-Whisperer/.superpowers/sdd/task-6-report.md
// and the "Fix round 1" section appended there. They're deliberately the
// *specific* alert wording, not generic words like "certificate" or
// "handshake", which also show up in an unrelated server-trust failure
// (e.g. "unable to get local issuer certificate") and would let this test
// pass for the wrong reason.
const (
	mtlsNoCertAlert  = "certificate required"
	mtlsForeignAlert = "unknown ca"
)

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

// testMTLSGate proves the relay's Caddy client_auth gate rejects connections
// that don't present an admitted client certificate, both with no cert at all
// and with a foreign self-signed cert.
func testMTLSGate(t *testing.T) {
	scenario(t, "the relay's Caddy client_auth gate admits only CA-issued client certs",
		"an HTTPS request with NO client cert is rejected with the 'certificate required' TLS alert",
		"an HTTPS request with a FOREIGN self-signed cert is rejected with the 'unknown ca' TLS alert",
		"neither rejection is a server-trust failure (the client does trust the relay's own server cert) — proving the gate, not a misconfig, is what refuses them")

	// No client cert: TLS handshake must be rejected by the client_auth gate.
	//
	// --http1.1: over the default h2 ALPN, curl defers sending the request
	// until after the (successful, TLS1.3) handshake, so the server's
	// certificate-required alert arrives mid-stream and curl only reports a
	// generic "getpeername() failed / Transport endpoint is not connected" /
	// "Broken pipe" — no "certificate" or "handshake" substring, even though
	// the rejection is real (confirmed directly with openssl s_client:
	// "tlsv13 alert certificate required"). Forcing HTTP/1.1 makes curl
	// surface the OpenSSL alert text directly instead.
	out, err := execInOK("client", "curl -sS --max-time 10 --http1.1 https://"+domain+"/ 2>&1")
	if err == nil {
		fatalf(t, "HTTPS without a client cert unexpectedly succeeded:\n%s", out)
	}
	assertMTLSRejection(t, "no client cert", out, mtlsNoCertAlert)

	// Foreign CA: a self-signed cert must be rejected too.
	execIn(t, "client", `cd /tmp && openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 `+
		`-keyout fake.key -out fake.crt -days 1 -nodes -subj /CN=intruder 2>/dev/null`)
	out, err = execInOK("client",
		"curl -sS --max-time 10 --http1.1 --cert /tmp/fake.crt --key /tmp/fake.key https://"+domain+"/ 2>&1")
	if err == nil {
		fatalf(t, "HTTPS with a foreign-CA cert unexpectedly succeeded:\n%s", out)
	}
	assertMTLSRejection(t, "foreign-CA cert", out, mtlsForeignAlert)
}
