//go:build e2e

package e2e

import (
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
	for _, s := range steps {
		if !t.Run(s.name, s.fn) {
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

// The scenarios below are not yet implemented. Their Skip messages state the
// behaviour they will verify, so `go test -v` documents the intended coverage.
func testContexts(t *testing.T) {
	t.Skip("NOT YET IMPLEMENTED (Task 8): kubectl-style context switching — new/rename/delete/use-context, export/import round-trip, and current-context reflected by the running client")
}
func testSecondTenant(t *testing.T) {
	t.Skip("NOT YET IMPLEMENTED (Task 8): multi-tenancy — a second server enrolls on the same admin relay non-disruptively (no xray restart, admin not locked out), each tenant isolated (server A's client cannot reach server B)")
}
func testDashboard(t *testing.T) {
	t.Skip("NOT YET IMPLEMENTED (Task 9): the in-server dashboard serves, shows live status/logs over SSE, and the relay terminal works")
}
func testRelayResilience(t *testing.T) {
	t.Skip("NOT YET IMPLEMENTED (Task 9): the tunnel recovers after the relay's caddy/xray are restarted (reverse tunnel + client reconnect)")
}
func testTeardown(t *testing.T) {
	t.Skip("NOT YET IMPLEMENTED (Task 9): tw admin destroy tears the relay down and tw admin status reflects it")
}
