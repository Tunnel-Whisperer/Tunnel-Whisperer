//go:build e2e

// Package e2e drives the Docker Compose test topology from the host. All tw
// and relay processes run inside containers; this package only orchestrates
// via `docker compose exec` and asserts on the results. See
// docs/superpowers/specs/2026-07-18-local-e2e-compose-design.md.
package e2e

import (
	"os/exec"
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
