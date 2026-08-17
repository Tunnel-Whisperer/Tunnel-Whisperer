//go:build e2e

// Package e2e drives the Docker Compose test topology from the host. All tw
// and relay processes run inside containers; this package only orchestrates
// via `docker compose exec` and asserts on the results. See
// .claude/superpowers/specs/2026-07-18-local-e2e-compose-design.md.
package e2e

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	domain   = "relay.tw.test"
	relayIP  = "172.28.0.10"
	echoPort = "7777"
	userPort = "18080"
)

func compose(args ...string) *exec.Cmd {
	base := []string{"compose", "-f", "docker-compose.yaml"}
	return exec.Command("docker", append(base, args...)...)
}

// execIn runs a shell script in a service container and fails the test on error.
func execIn(t *testing.T, service, script string) string {
	t.Helper()
	out, err := execInOK(service, script)
	if err != nil {
		dumpDiagnostics(t)
		t.Fatalf("exec in %s failed: %v\nscript: %s\noutput:\n%s", service, err, script, out)
	}
	return out
}

func execInOK(service, script string) (string, error) {
	out, err := compose("exec", "-T", service, "sh", "-c", script).CombinedOutput()
	return string(out), err
}

// execDetached starts a long-running process in a container and returns
// immediately (docker compose exec -d).
func execDetached(t *testing.T, service, script string) {
	t.Helper()
	if out, err := compose("exec", "-T", "-d", service, "sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("detached exec in %s failed: %v\n%s", service, err, out)
	}
}

// scenario logs a human-readable description of what the running scenario
// verifies, so `go test -v` output is self-documenting about the product
// behaviour under test — not just the Go function name. `summary` is a
// one-line "what this proves"; each `check` is a specific assertion the
// scenario makes. Emitted as the first lines of every subtest.
func scenario(t *testing.T, summary string, checks ...string) {
	t.Helper()
	if current != nil {
		current.summary = summary
		current.checks = checks
	}
	t.Logf("SCENARIO — %s", summary)
	t.Logf("  (real data path: client → Xray VLESS/XHTTP/mTLS :443 → Caddy client_auth gate → relay Xray → reverse SSH → server)")
	for _, c := range checks {
		t.Logf("  ✓ %s", c)
	}
}

// waitForRelayBoot blocks until the relay's systemd has finished booting.
// `docker compose ps --status running` only proves PID 1 (/sbin/init) is alive,
// not that the system manager has reached its boot target — and the real
// install script runs `systemctl enable/restart` (xray, caddy) under
// `set -euo pipefail`, which aborts with "Failed to connect to bus" if dbus/the
// manager isn't up yet. That was the cold-`make e2e` flake: RelayInstall fired
// before boot completed. systemd's own signal is authoritative: is-system-running
// prints "running" (or "degraded" — booted, some unit failed, still usable) once
// the boot transaction is done; "initializing"/"starting" while it isn't. It
// exits non-zero for "degraded", so gate on the printed state, not the exit code.
func waitForRelayBoot(t *testing.T) {
	t.Helper()
	waitFor(t, "relay systemd boot", 120*time.Second, func() (bool, string) {
		out, _ := execInOK("relay", "systemctl is-system-running 2>/dev/null || true")
		state := strings.TrimSpace(out)
		if state == "running" || state == "degraded" {
			return true, state
		}
		return false, "systemd state: " + state
	})
}

// waitFor polls cond every 2s until it reports done or the timeout elapses.
func waitFor(t *testing.T, desc string, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		done, status := cond()
		if done {
			return
		}
		last = status
		time.Sleep(2 * time.Second)
	}
	dumpDiagnostics(t)
	t.Fatalf("timed out after %s waiting for %s (last: %s)", timeout, desc, last)
}

func dumpDiagnostics(t *testing.T) {
	t.Helper()
	for _, c := range [][]string{
		{"ps"},
		{"logs", "--tail", "200"},
		{"exec", "-T", "relay", "journalctl", "-u", "caddy", "-u", "xray", "--no-pager", "-n", "100"},
	} {
		out, _ := compose(c...).CombinedOutput()
		t.Logf("--- docker compose %v ---\n%s", c, out)
	}
}

// killMatching kills every process in service whose /proc/<pid>/cmdline
// contains substr, skipping the shell script's own PID (its own
// /proc/self/cmdline literally contains the search text when substr matches
// the invoking command, so it would otherwise match itself). The tw image
// ships no pkill/ps (no procps), so this walks /proc directly. Non-fatal: a
// container with nothing to kill is the common case, not an error.
func killMatching(t *testing.T, service, substr string) {
	t.Helper()
	script := `for p in /proc/[0-9]*; do ` +
		`pid=${p#/proc/}; ` +
		`[ "$pid" = "$$" ] && continue; ` +
		`cmd=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null) || continue; ` +
		`case "$cmd" in *"` + substr + `"*) kill -9 "$pid" 2>/dev/null ;; esac; ` +
		`done`
	if out, err := execInOK(service, script); err != nil {
		t.Logf("killMatching(%s, %q): %v\n%s", service, substr, err, out)
	}
}

// twServices are the containers that run the tw binary.
var twServices = []string{"admin", "server", "client", "server2"}

// fatalf fails the test after dumping full topology diagnostics.
func fatalf(t *testing.T, format string, args ...any) {
	t.Helper()
	dumpDiagnostics(t)
	t.Fatalf(format, args...)
}

// localCertsShim prepends a `{ local_certs }` global block to the relay
// Caddyfile so Caddy issues certs from its internal CA instead of reaching for
// ACME (unreachable in the offline test network), then restarts Caddy. Any
// relay-side operation that re-renders the Caddyfile from scratch (the
// install script, EnrollServer, un-enroll) wipes this, so it must be
// re-applied after every one of those. Idempotent: a no-op once the block is
// present. Lives here (not in a _test.go file) because runInviteExchange
// below — itself in this non-test file, so it's linkable from `go build`,
// unlike the test-only helpers in the *_test.go files — calls it directly.
func localCertsShim(t *testing.T) {
	t.Helper()
	execIn(t, "relay", `grep -q local_certs /etc/caddy/Caddyfile || `+
		`(printf '{\n\tlocal_certs\n}\n' | cat - /etc/caddy/Caddyfile > /tmp/Caddyfile.new `+
		`&& mv /tmp/Caddyfile.new /etc/caddy/Caddyfile && systemctl restart caddy)`)
}

// inviteCodeRe / inviteSASRe / inviteExitRe are the literal print formats of
// the issuer/enrollee CLIs (internal/cli/relay_invite.go, invite_user.go,
// join.go): "Invite code: <code>", "SAS: XXX-XXX" (base32 uppercase, on both
// sides), and the synthetic completion sentinel runInviteExchange appends.
var (
	inviteCodeRe = regexp.MustCompile(`Invite code: (\S+)`)
	inviteSASRe  = regexp.MustCompile(`SAS: ([A-Z2-7]{3}-[A-Z2-7]{3})`)
	inviteExitRe = regexp.MustCompile(`TW_INVITE_EXIT (\d+)`)
)

// runInviteExchange drives one full invite/join exchange end to end:
// issuerCmd (e.g. "tw relay invite" or "tw server user invite bob -m ...")
// runs detached on issuerService and answers its own [y/N] SAS prompt once
// the human read-back has been cross-checked against the enrollee's side;
// joinCmd (containing a literal "{code}" placeholder) runs detached on
// joinService once the code is minted. Returns both commands' full combined
// output for scenario-specific assertions.
//
// stdin plumbing: execDetached has no persistent stdin channel, and the
// issuer needs to answer a prompt that only appears *after* it has already
// printed the invite code — so a plain `issuerCmd < someFile` won't work
// (opening a FIFO for read-only blocks until a writer connects, which would
// delay "Invite code: ..." until we're ready to answer, too late — we need
// the code first to unblock the join side). The fix is the classic FIFO
// non-blocking-open trick: the wrapper shell opens the FIFO itself,
// read-write, onto fd 9 (`exec 9<>fifo` — Linux treats O_RDWR opens on a
// FIFO as always succeeding, unlike plain O_RDONLY/O_WRONLY), then runs the
// issuer command with its stdin duped from fd 9. That satisfies the "a
// writer exists" condition immediately, so the command's own stdin open
// never blocks either. A later `echo y > fifo` (opened write-only) then also
// never blocks, since a reader (the command, via fd 9) is already present;
// the byte just sits in the pipe buffer until the command's own read() call
// (at the SAS prompt) consumes it.
//
// Completion signal: `tw relay invite` prints a final "Server ... enrolled"
// line, but `tw server user invite` prints nothing on success (see
// task-11-report.md) — so instead of scraping for success/denial text, the
// wrapper always appends a synthetic "TW_INVITE_EXIT <code>" sentinel after
// the command exits, regardless of outcome; that is the one signal common to
// every invite-issuing command.
//
// Caddyfile reload: `tw relay invite` (server-role) calls the same
// EnrollServer used by the old add-server/enroll-server flows, which
// re-renders and reloads the relay's Caddyfile *after* SAS approval — wiping
// the suite's local_certs shim — while `tw server user invite`
// (client-role) never touches Caddy at all. Rather than push that
// product-specific knowledge onto every caller, the final wait loop below
// watches for the "Caddyfile reloaded" progress line and reapplies the shim
// itself if it ever appears; for the client-role case that branch is simply
// never taken.
func runInviteExchange(t *testing.T, issuerService, issuerCmd, joinService, joinCmd string) (string, string) {
	t.Helper()
	const (
		fifo      = "/tmp/tw-approve"
		issuerLog = "/shared/invite-issuer.log"
		joinLog   = "/shared/invite-join.log"
	)

	execIn(t, issuerService, "rm -f "+fifo+" "+issuerLog)
	issuerScript := "mkfifo -m 600 " + fifo + " && exec 9<>" + fifo + " && (" +
		issuerCmd + " <&9 > " + issuerLog + " 2>&1; echo \"TW_INVITE_EXIT $?\" >> " + issuerLog + ")"
	execDetached(t, issuerService, issuerScript)

	var code string
	waitFor(t, "invite code minted ("+issuerCmd+")", 30*time.Second, func() (bool, string) {
		out, _ := execInOK(issuerService, "cat "+issuerLog+" 2>/dev/null")
		if m := inviteCodeRe.FindStringSubmatch(out); m != nil {
			code = m[1]
			return true, ""
		}
		return false, out
	})

	execIn(t, joinService, "rm -f "+joinLog)
	execDetached(t, joinService, strings.ReplaceAll(joinCmd, "{code}", code)+" > "+joinLog+" 2>&1")

	var issuerSAS, joinSAS string
	waitFor(t, "both SAS strings shown", 60*time.Second, func() (bool, string) {
		iOut, _ := execInOK(issuerService, "cat "+issuerLog+" 2>/dev/null")
		jOut, _ := execInOK(joinService, "cat "+joinLog+" 2>/dev/null")
		im, jm := inviteSASRe.FindStringSubmatch(iOut), inviteSASRe.FindStringSubmatch(jOut)
		if im == nil || jm == nil {
			return false, iOut + "\n---\n" + jOut
		}
		issuerSAS, joinSAS = im[1], jm[1]
		return true, ""
	})
	if issuerSAS != joinSAS {
		fatalf(t, "SAS mismatch: issuer %q vs enrollee %q — MITM or protocol bug", issuerSAS, joinSAS)
	}
	execIn(t, issuerService, "echo y > "+fifo)

	shimApplied := false
	waitFor(t, "issuer command finished ("+issuerCmd+")", 120*time.Second, func() (bool, string) {
		out, _ := execInOK(issuerService, "cat "+issuerLog+" 2>/dev/null")
		if !shimApplied && strings.Contains(out, "Caddyfile reloaded") {
			shimReapplyStart := time.Now()
			localCertsShim(t)
			waitFor(t, "caddy local root CA after invite's Caddyfile reload", 30*time.Second, func() (bool, string) {
				crtOut, err := execInOK("relay", "cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt")
				if err != nil || !strings.Contains(crtOut, "BEGIN CERTIFICATE") {
					return false, "root.crt not present yet"
				}
				return true, ""
			})
			elapsed := time.Since(shimReapplyStart)
			t.Logf("shim reapply confirmed live after %s (racing EnrollServer's SSH-dial retry budget)", elapsed)
			if elapsed > 10*time.Second {
				t.Errorf("shim reapply took %s — less than 5s of margin left against EnrollServer's ~15s SSH-dial retry budget; near-miss, investigate before it flakes", elapsed)
			}
			shimApplied = true
		}
		return inviteExitRe.MatchString(out), out
	})

	iOut, _ := execInOK(issuerService, "cat "+issuerLog+" 2>/dev/null")
	jOut, _ := execInOK(joinService, "cat "+joinLog+" 2>/dev/null")
	if m := inviteExitRe.FindStringSubmatch(iOut); m != nil && m[1] != "0" {
		t.Logf("issuer command %q exited %s (non-fatal here — caller asserts on log content)", issuerCmd, m[1])
	}
	return iOut, jOut
}
