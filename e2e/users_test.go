//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// testUserLifecycle creates a user on the server, exports it as a client
// context bundle, imports it on the client, connects, and proves byte-for-byte
// traffic through the relay + tunnel.
func testUserLifecycle(t *testing.T) {
	scenario(t, "a user is created on the server, shipped to a client as a context bundle, and moves real bytes through the tunnel",
		"tw server user create/apply/list registers alice with her port mapping",
		"tw config export-user packages her as a .twctx bundle; the client imports + activates it",
		"tw client connect opens the local tunnel port",
		"a byte-for-byte echo round-trip (hello-tw-e2e) succeeds through relay + tunnel",
		"tw client test: all three steps pass (DNS, HTTPS/mTLS, and an SSH auth handshake to the server's embedded SSH through the tunnel)",
		"tab completion: tw __complete server user delete offers alice")

	// Re-runnability: a prior full-suite run may have left a live
	// `tw client connect` and an old /etc/tw-test on the client, and a
	// leftover `alice` on the server (e.g. if a later scenario in that run
	// failed before Revocation cleaned her up). Wipe/kill defensively before
	// creating anything new.
	t.Log("pre-cleanup: killing any stale 'tw client connect' on the client")
	killMatching(t, "client", "tw client connect")
	t.Log("pre-cleanup: wiping client's /etc/tw-test for a clean import")
	execIn(t, "client", "rm -rf /etc/tw-test")
	if out, err := execInOK("server", "printf 'y\\n' | tw server user delete alice"); err != nil {
		t.Logf("pre-cleanup: no leftover alice on the server (or delete failed, non-fatal): %v\n%s", err, out)
	} else {
		t.Logf("pre-cleanup: deleted a leftover alice from a previous run:\n%s", out)
	}

	// Create + register + list (server daemon is running; CLI goes via gRPC where wired).
	execIn(t, "server", "tw server user create alice -m "+userPort+":"+echoPort)
	execIn(t, "server", "tw server user apply alice")
	out := execIn(t, "server", "tw server user list")
	if !strings.Contains(out, "alice") {
		fatalf(t, "alice missing from user list:\n%s", out)
	}

	// Tab completion offers the created user for user-selecting commands.
	compOut := execIn(t, "server", `tw __complete server user delete ""`)
	if !strings.Contains(compOut, "alice") {
		fatalf(t, "user delete completion does not offer alice:\n%s", compOut)
	}

	// Export as a client context bundle; import + activate on the client.
	execIn(t, "server", "cd /shared && rm -f alice-tw-context.twctx && tw config export-user alice")
	execIn(t, "client", "tw config import /shared/alice-tw-context.twctx --activate")
	// Without --name, a client bundle's context is named after its user — the
	// self-explanatory default (not the relay domain).
	curCtx := execIn(t, "client", "tw config current-context")
	if !strings.Contains(curCtx, "alice") {
		fatalf(t, "imported context not named after the user: current-context = %q, want alice", curCtx)
	}

	// The imported client bundle's mode is tamper-evidently signed.
	if viewOut := execIn(t, "client", "tw config view"); !strings.Contains(viewOut, "mode_auth:") {
		fatalf(t, "imported client profile is not mode-signed (no mode_auth: block):\n%s", viewOut)
	}

	// The client-role gate refuses a server-only command outright — proving
	// requireMode's cross-role check, not just the mode-signature check
	// exercised in ServerJoin's tamper test.
	if gateOut, gateErr := execInOK("client", "tw server join-relay relay.example"); gateErr == nil {
		fatalf(t, "client profile was allowed to run a server-mode command:\n%s", gateOut)
	} else if !strings.Contains(gateOut, "requires server mode") {
		fatalf(t, "expected a server-mode gate error (root.go modeError), got:\n%s", gateOut)
	}

	execIn(t, "client", "tw client listen") // prints current listen address; covers the command

	// Connect and prove byte-for-byte traffic through relay + tunnel.
	execDetached(t, "client", "tw client connect > /var/log/tw-client.log 2>&1")
	waitFor(t, "client tunnel listening", 120*time.Second, func() (bool, string) {
		_, err := execInOK("client", "nc -z 127.0.0.1 "+userPort)
		if err != nil {
			out, _ := execInOK("client", "tail -5 /var/log/tw-client.log")
			return false, out
		}
		return true, ""
	})
	echoOut := execIn(t, "client",
		"printf 'hello-tw-e2e' | nc -w 10 127.0.0.1 "+userPort)
	if strings.TrimSpace(echoOut) != "hello-tw-e2e" {
		fatalf(t, "echo round-trip mismatch: %q", echoOut)
	}

	execIn(t, "client", "tw client status")

	// All three steps must pass for a client role: DNS, HTTPS/mTLS, and an
	// SSH auth handshake to the SERVER's embedded SSH through the tunnel.
	// (Step 3 previously dialed the relay VM's sshd with the client key — a
	// wiring bug this suite documented as a known deviation; now fixed.)
	out = execIn(t, "client", "tw client test")
	for _, want := range []string{"[1/3] DNS —", "[2/3] HTTPS (Caddy) —", "[3/3] Xray + SSH (server auth) —"} {
		if !strings.Contains(out, want) {
			fatalf(t, "tw client test: step %q missing or failed:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unable to authenticate") || strings.Contains(out, "✗") {
		fatalf(t, "tw client test reported a failure:\n%s", out)
	}
}

// permitOpenProbePort is a local port used only to probe single-session
// enforcement without tripping the client-side bind preflight added by
// PortOverride — see the deviation note inside testPermitOpen. Distinct from
// userPort (18080), bob's 18081 (RelayResilience), and PortOverride's
// 18090/18091.
const permitOpenProbePort = "18092"

// testPermitOpen asserts the server-side gate: alice's authorized_keys entry
// carries only the granted permitopen target plus single-session, and a
// second concurrent connect for the same user is genuinely rejected by the
// single-session mechanism (verified from the server's own log, not just
// inferred from the client's exit status).
func testPermitOpen(t *testing.T) {
	scenario(t, "the server-side SSH gate confines a user to exactly their granted target and one session",
		"alice's authorized_keys entry carries permitopen for ONLY her granted port (not the server SSH port 2222)",
		"the entry carries the single-session option",
		"a second concurrent connect for alice is rejected — confirmed from the server log's 'single-session: rejecting duplicate connection', not just the client's exit code")

	// Enable via the CLI (this used to be reachable only through the
	// dashboard API — the documented product gap, now closed).
	execIn(t, "server", "tw server user single-session alice on")
	ssOut := execIn(t, "server", "tw server user single-session alice")
	if !strings.Contains(ssOut, "on") {
		fatalf(t, "single-session state not reported as on:\n%s", ssOut)
	}

	// The authorized_keys entry for alice must carry ONLY the granted
	// permitopen target plus single-session — that's the server-side gate
	// (re-read on every auth attempt).
	ak := execIn(t, "server", "cat /etc/tw-test/authorized_keys")
	line := ""
	for _, l := range splitLines(ak) {
		if strings.Contains(l, "alice@tw") {
			line = l
			break
		}
	}
	if line == "" {
		fatalf(t, "no authorized_keys entry for alice:\n%s", ak)
	}
	if !strings.Contains(line, `permitopen="127.0.0.1:`+echoPort+`"`) {
		fatalf(t, "alice entry missing permitopen for the granted port: %s", line)
	}
	if strings.Contains(line, `permitopen="127.0.0.1:2222"`) {
		fatalf(t, "alice entry grants the server SSH port — must not: %s", line)
	}
	if !strings.Contains(line, "single-session") {
		fatalf(t, "alice entry missing single-session: %s", line)
	}

	// single-session at runtime: a second concurrent connect for the same
	// user must fail while the first is up. Observed live: the SSH protocol
	// never surfaces the server's specific rejection reason to the client —
	// golang.org/x/crypto/ssh just reports the generic "ssh: unable to
	// authenticate, attempted methods [none publickey], no supported methods
	// remain" — so neither "single-session" nor "rejected" ever appears in
	// the client's own output; the brief's substring check would not have
	// been able to tell single-session working from any other auth failure.
	// The server's log is authoritative here: internal/ssh/server.go emits
	// `single-session: rejecting duplicate connection tw_user=alice` at the
	// moment it rejects the second auth attempt. Confirm the client attempt
	// genuinely didn't connect, then confirm the server's log shows the
	// single-session mechanism (not some other failure) is what stopped it.
	//
	// Deviation (added after PortOverride, see task-6-report.md): plain
	// `tw client connect` here would now be rejected earlier than the SSH
	// layer. PortOverride's new client-side bind preflight (internal/ops/
	// client.go, preflightBind) test-binds every local port before touching
	// SSH, and alice's default local port (userPort) is already held by the
	// FIRST, still-live connection this scenario depends on — so a second
	// plain `tw client connect` now fails at "Config validation" with the
	// preflight's own "already in use" message, never reaching the SSH auth
	// step this test means to exercise. That's the client-side feature
	// working exactly as designed (real per-machine local-port conflicts
	// really should fail fast) — single-session enforcement itself is a
	// server-side, per-user check keyed on the SSH identity and is
	// independent of which local port the client binds. Route the second
	// attempt through --map onto a free local port (permitOpenProbePort,
	// unused elsewhere) so it clears the client-side preflight and actually
	// reaches the server's public-key callback, where single-session must
	// reject it.
	out, err := execInOK("client", "timeout 20 tw client connect --map "+permitOpenProbePort+":"+echoPort+" 2>&1")
	if err == nil {
		fatalf(t, "second concurrent session's `tw client connect` exited 0 (expected timeout/error):\n%s", out)
	}
	logOut := execIn(t, "server", "grep -c 'single-session: rejecting duplicate connection' /var/log/tw-server.log || true")
	if strings.TrimSpace(logOut) == "0" || strings.TrimSpace(logOut) == "" {
		fatalf(t, "server log shows no single-session rejection for the second concurrent connect (client output below):\n%s", out)
	}
	t.Logf("second concurrent connect rejected; server log confirms %s single-session rejection(s)", strings.TrimSpace(logOut))
}

// testRevocation unregisters and deletes alice on the server, then proves a
// fresh client connect fails WITHOUT any server restart (authorized_keys is
// re-read on every auth attempt). Leaves the client disconnected and alice
// deleted — later scenarios must not assume alice works.
func testRevocation(t *testing.T) {
	scenario(t, "revoking a user takes effect live, with no server restart",
		"tw server user unregister + delete removes alice",
		"a fresh tw client connect for alice never opens its tunnel port across a full 30s poll — proving authorized_keys is re-read on every auth attempt (no restart needed)")

	// Unregister from the relay, then delete. Both prompt [y/N].
	execIn(t, "server", "printf 'y\\n' | tw server user unregister alice")
	execIn(t, "server", "printf 'y\\n' | tw server user delete alice")

	// Kill the client's live connection, then prove a fresh connect fails
	// WITHOUT any server restart (authorized_keys re-read per auth attempt).
	killMatching(t, "client", "tw client connect")
	execDetached(t, "client", "tw client connect > /var/log/tw-client-revoked.log 2>&1")

	// waitFor returns success on the FIRST failed probe, which is too weak
	// here — the tunnel may just be slow to come up, and a probe that
	// happens to land in that startup window would pass the test for the
	// wrong reason. Poll explicitly for 30s and require the port to NEVER
	// answer during that window; fail immediately if it ever does.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err == nil {
			out, _ := execInOK("client", "tail -20 /var/log/tw-client-revoked.log")
			fatalf(t, "revoked client's tunnel port answered — revocation did not take effect:\n%s", out)
		}
		time.Sleep(2 * time.Second)
	}
	out, _ := execInOK("client", "tail -5 /var/log/tw-client-revoked.log")
	t.Logf("revoked client stayed down for 30s as expected; last log lines:\n%s", out)

	killMatching(t, "client", "tw client connect")
}
