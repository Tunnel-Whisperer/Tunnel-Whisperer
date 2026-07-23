//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
)

// TestMain-free: `make e2e` is responsible for compose up/down. TestE2E only
// verifies the topology is up, then runs the scenarios in dependency order.
// Scenarios share container state; use -run 'TestE2E/<Name>' only against an
// already-provisioned topology (E2E_KEEP=1).
func TestE2E(t *testing.T) {
	out, err := compose("ps", "--status", "running", "--services").CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose ps failed — did you run `make e2e`? %v\n%s", err, out)
	}
	for _, svc := range append([]string{"relay"}, twServices...) {
		if !containsLine(string(out), svc) {
			t.Fatalf("service %q is not running — start the topology with `make e2e`\n%s", svc, out)
		}
	}

	// `compose ps` only proves the containers' PID 1 is alive. The relay runs
	// systemd, and RelayInstall's install script drives systemctl under set -e —
	// wait for the boot transaction to complete before any scenario runs, or a
	// cold `make e2e` races the install against a half-booted manager.
	waitForRelayBoot(t)

	steps := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"Smoke", testSmoke},
		{"RelayInstall", testRelayInstall},
		{"ServerJoin", testServerJoin},
		{"MTLSGate", testMTLSGate},
		{"UserLifecycle", testUserLifecycle},
		{"PermitOpen", testPermitOpen},
		{"Revocation", testRevocation},
		{"Contexts", testContexts},
		{"SecondTenant", testSecondTenant},
		{"Dashboard", testDashboard},
		{"RelayResilience", testRelayResilience},
		{"Teardown", testTeardown},
	}
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.name
	}
	initReport(names)
	defer writeReport(t)
	for _, s := range steps {
		if !runScenario(t, s.name, s.fn) {
			t.Fatalf("scenario %s failed; later scenarios depend on it — stopping", s.name)
		}
	}
}

func containsLine(haystack, want string) bool {
	for _, l := range splitLines(haystack) {
		if l == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' || r == '\r' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// testSmoke proves the harness itself: tw runs in every tw container and the
// relay resolves.
func testSmoke(t *testing.T) {
	scenario(t, "the harness itself works: tw runs in every role container and the relay is reachable",
		"the tw binary executes in admin/server/client/server2",
		"relay.tw.test resolves over the compose network",
		"the relay's sshd is active")
	for _, svc := range twServices {
		out := execIn(t, svc, "tw --version")
		t.Logf("%s: %s", svc, out)
	}
	execIn(t, "admin", "getent hosts "+domain)
	execIn(t, "relay", "systemctl is-active ssh || systemctl is-active sshd")
}

// testContexts drives the kubectl-style context store: identity columns in
// get-contexts and the short-ID selector, plus the new/rename/delete/use
// lifecycle. Runs on the admin container (its "default" context is restored
// as current at the end) and reads the client container's context imported in
// UserLifecycle. Still deferred to full Task 8: export/import round-trip and
// a running client reconnecting on switch.
func testContexts(t *testing.T) {
	scenario(t, "contexts are listable with identity columns and switchable by short ID",
		"tw config get-contexts shows ID/ROLE/USER/RELAY; the client's context row shows USER alice and an 8-hex ID",
		"tw config new-context creates and switches to a fresh empty context (the admin's original is preserved)",
		"tw config use-context <id> switches back by the short ID alone",
		"tw config rename-context and delete-context clean up the scratch context",
		"tw config current-context reflects each switch")

	// Client: the context imported from alice's user bundle must show the ssh
	// user and a stable short ID (USER column is filled for client contexts).
	out := execIn(t, "client", "tw config get-contexts")
	if !regexp.MustCompile(`(?m)^\*\s+\S+\s+[0-9a-f]{8}\s+client\s+alice\s+`).MatchString(out) {
		fatalf(t, "client current-context row does not show an 8-hex ID and USER alice:\n%s", out)
	}

	// Admin: capture the current context's name and short ID from the listing.
	// The relay role is now named "relay" (renamed from "admin" — see
	// docs/superpowers/specs/2026-07-22-mode-integrity-design.md).
	out = execIn(t, "admin", "tw config get-contexts")
	row := regexp.MustCompile(`(?m)^\*\s+(\S+)\s+([0-9a-f]{8})\s+relay\s+`).FindStringSubmatch(out)
	if row == nil {
		fatalf(t, "admin current-context row missing name or 8-hex ID:\n%s", out)
	}
	name, id := row[1], row[2]

	// The relay profile's mode is tamper-evidently signed (mode_auth block).
	if viewOut := execIn(t, "admin", "tw config view"); !strings.Contains(viewOut, "mode_auth:") {
		fatalf(t, "relay profile is not mode-signed (no mode_auth: block):\n%s", viewOut)
	}

	// New empty context becomes current; the original is preserved (sealed).
	execIn(t, "admin", "tw config new-context scratch")
	if cur := execIn(t, "admin", "tw config current-context"); !strings.Contains(cur, "scratch") {
		fatalf(t, "current-context after new-context = %q, want scratch", cur)
	}

	// Switch back by short ID alone — the point of the ID column.
	execIn(t, "admin", "tw config use-context "+id)
	if cur := execIn(t, "admin", "tw config current-context"); !strings.Contains(cur, name) {
		fatalf(t, "use-context %s did not switch back to %q:\n%s", id, name, cur)
	}

	// Rename and delete the scratch context; the listing must end clean.
	execIn(t, "admin", "tw config rename-context scratch scratch2")
	execIn(t, "admin", "tw config delete-context scratch2")
	out = execIn(t, "admin", "tw config get-contexts")
	if strings.Contains(out, "scratch") {
		fatalf(t, "scratch context still listed after rename+delete:\n%s", out)
	}
	// The admin's context must still hold its identity after the round-trip.
	if !strings.Contains(out, id) {
		fatalf(t, "admin context lost its ID %s after the switch round-trip:\n%s", id, out)
	}
}
// The scenarios below are not yet implemented. Their Skip messages state the
// behaviour they will verify, so `go test -v` documents the intended coverage.
func testDashboard(t *testing.T) {
	skipScenario(t, "NOT YET IMPLEMENTED (Task 9): the in-server dashboard serves, shows live status/logs over SSE, and the relay terminal works")
}
func testRelayResilience(t *testing.T) {
	skipScenario(t, "NOT YET IMPLEMENTED (Task 9): the tunnel recovers after the relay's caddy/xray are restarted (reverse tunnel + client reconnect)")
}
func testTeardown(t *testing.T) {
	skipScenario(t, "NOT YET IMPLEMENTED (Task 9): tw relay destroy tears the relay down and tw relay status reflects it")
}
