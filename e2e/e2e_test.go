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
	for _, svc := range twServices {
		out := execIn(t, svc, "tw --version")
		t.Logf("%s: %s", svc, out)
	}
	execIn(t, "admin", "getent hosts "+domain)
	execIn(t, "relay", "systemctl is-active ssh || systemctl is-active sshd")
}

func testServerJoin(t *testing.T)      { t.Skip("implemented in Task 6") }
func testMTLSGate(t *testing.T)        { t.Skip("implemented in Task 6") }
func testUserLifecycle(t *testing.T)   { t.Skip("implemented in Task 7") }
func testPermitOpen(t *testing.T)      { t.Skip("implemented in Task 7") }
func testRevocation(t *testing.T)      { t.Skip("implemented in Task 7") }
func testContexts(t *testing.T)        { t.Skip("implemented in Task 8") }
func testSecondTenant(t *testing.T)    { t.Skip("implemented in Task 8") }
func testDashboard(t *testing.T)       { t.Skip("implemented in Task 9") }
func testRelayResilience(t *testing.T) { t.Skip("implemented in Task 9") }
func testTeardown(t *testing.T)        { t.Skip("implemented in Task 9") }
