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
		"tw client test steps 1-2 (DNS, HTTPS/mTLS) pass; step 3's known client-role auth failure is asserted stable (loud if it changes)")

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

	// Export as a client context bundle; import + activate on the client.
	execIn(t, "server", "cd /shared && rm -f alice-tw-context.twctx && tw config export-user alice")
	execIn(t, "client", "tw config import /shared/alice-tw-context.twctx --activate")
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

	// Deviation from the brief: `tw client test` step 3 ("Xray + SSH") always
	// fails for a legitimate client user, so we can't gate on the whole
	// command succeeding as the brief's original waitFor did. sharedTestRelay
	// (internal/cli/test_relay.go) is shared verbatim across admin/server/
	// client and its step 3 calls withRelaySSH (internal/ops/user.go), which
	// unconditionally dials the RELAY VM's own SSH (cfg.Server.RelaySSHUser,
	// default "ubuntu", port 22 — those defaults survive for a client config
	// because config.Load() starts from Default() and the client's
	// config.yaml has no `server:` section to override them) using this
	// role's <config dir>/id_ed25519. For a client that key is alice's
	// per-user tunnel key, generated for the server's SSH and authorized
	// only in the server's authorized_keys — it was never, and per the relay
	// SSH security model (tenants are forward-only; only admin shells into
	// the relay) should never be, added to the relay VM's own
	// ~ubuntu/.ssh/authorized_keys. So step 3 deterministically fails with
	// "ssh: unable to authenticate ... no supported methods remain" — proven
	// live during this task (see task-7-report.md). That is a pre-existing
	// product-CLI wiring bug (clientTestCmd reusing the admin/server relay
	// check unmodified), not an e2e issue, and fixing it is out of scope
	// here (no product-code changes for this task). We still run the
	// command — it covers the "client test" coverage.yaml entry per the
	// brief's intent — but only assert on the two steps that are actually
	// meaningful for a client role (DNS, HTTPS/mTLS); step 3's known failure
	// is logged, not asserted on, so this scenario doesn't become hostage to
	// an unrelated, already-documented bug.
	out = execIn(t, "client", "tw client test")
	if !strings.Contains(out, "[1/3] DNS —") {
		fatalf(t, "tw client test: DNS step did not report success:\n%s", out)
	}
	if !strings.Contains(out, "[2/3] HTTPS (Caddy) —") {
		fatalf(t, "tw client test: HTTPS step did not report success:\n%s", out)
	}
	if strings.Contains(out, "unable to authenticate") {
		t.Logf("tw client test: step 3 (Xray+SSH) failed as expected (known pre-existing bug, see task-7-report.md):\n%s", out)
	} else {
		t.Errorf("tw client test step 3 changed behavior: expected the known client-role auth failure ('unable to authenticate', see task-7-report.md); got:\n%s", out)
	}
}

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

	// Deviation from the brief: `tw server user create` hardcodes
	// singleSession=false (internal/ops/user.go, CreateUser's
	// appendAuthorizedKey call) and there is currently no CLI command that
	// can turn it on — SetUserSingleSession (internal/ops/user.go) is only
	// reachable via the dashboard's PUT /api/users/<name>/single-session
	// (internal/dashboard/handlers_api.go, apiUserSingleSession; found to be
	// unauthenticated). So a plain `tw server user create` alice does NOT
	// produce a single-session entry — confirmed live: the authorized_keys
	// line right after create+apply was
	// `permitopen="127.0.0.1:7777" ssh-ed25519 ... alice@tw` with no
	// single-session option. This looks like a real product gap (a
	// documented security feature, see CLAUDE.md and
	// relay-ssh-security-model memory, with zero CLI reachability) worth
	// flagging — see task-7-report.md — but fixing it is out of scope here
	// (no product-code changes for this task). The dashboard API is the only
	// currently-reachable path, so use it to turn single-session on for
	// alice, which lets this scenario genuinely exercise the feature.
	dashOut := execIn(t, "server",
		`curl -sS -X PUT -d '{"enabled":true}' http://127.0.0.1:8080/api/users/alice/single-session`)
	if !strings.Contains(dashOut, `"status":"ok"`) {
		fatalf(t, "enabling single-session for alice via the dashboard API failed:\n%s", dashOut)
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
	out, err := execInOK("client", "timeout 20 tw client connect 2>&1")
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
