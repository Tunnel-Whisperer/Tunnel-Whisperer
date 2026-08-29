//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// bearer injects the dashboard auth header, fetching each container's own token
// inline. The dashboard requires a token (Authorization: Bearer …) or a login
// session on every route except /login and /static.
const bearer = ` -H "Authorization: Bearer $(tw dashboard token)" `

// testDashboard proves the dashboard serves per-role and mirrors the CLI:
// the server daemon's dashboard reports status, and the admin's dashboard
// exposes tenant management (live server table) and the context store.
// Read-only apart from a bogus un-enroll that must be rejected. It also proves
// the token gate: unauthenticated requests are rejected, and `tw dashboard
// token` yields a token that unlocks the API.
func testDashboard(t *testing.T) {
	scenario(t, "the dashboard serves per-role and mirrors CLI features behind a token gate",
		"unauthenticated requests to the server dashboard are rejected (401)",
		"tw dashboard token prints a token that unlocks the API and the status page reports mode server",
		"tw dashboard on the admin serves the relay home and the Servers page (authenticated)",
		"admin /api/servers lists server-1 with its live tunnel up (mirrors tw relay get-servers)",
		"admin /api/config/contexts returns the current context",
		"a bogus un-enroll POST is rejected and get-servers still lists server-1")

	// The token gate: without a token, the API is closed.
	code := strings.TrimSpace(execIn(t, "server",
		"curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/status"))
	if code != "401" {
		fatalf(t, "unauthenticated /api/status should be 401, got %s", code)
	}
	// The login page itself is reachable without a token.
	code = strings.TrimSpace(execIn(t, "server",
		"curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/login"))
	if code != "200" {
		fatalf(t, "/login should be reachable unauthenticated, got %s", code)
	}
	if tok := strings.TrimSpace(execIn(t, "server", "tw dashboard token")); tok == "" {
		fatalf(t, "tw dashboard token printed nothing")
	}

	// Server: the daemon started in ServerJoin already serves the dashboard.
	out := execIn(t, "server", "curl -sf"+bearer+"http://127.0.0.1:8080/")
	if !strings.Contains(out, "Tunnel Whisperer") {
		fatalf(t, "server dashboard status page did not render:\n%.400s", out)
	}
	out = execIn(t, "server", "curl -sf"+bearer+"http://127.0.0.1:8080/api/status")
	if !strings.Contains(out, `"server"`) {
		fatalf(t, "server /api/status does not report mode server:\n%s", out)
	}

	// Admin: no daemon runs there — start the standalone dashboard. Pre-create
	// the token before launching so the daemon and our curls share one value.
	execIn(t, "admin", "tw dashboard token >/dev/null")
	killMatching(t, "admin", "tw dashboard")
	execDetached(t, "admin", "tw dashboard > /shared/dash-admin.log 2>&1")
	defer killMatching(t, "admin", "tw dashboard")
	waitFor(t, "admin dashboard up", 30*time.Second, func() (bool, string) {
		out, err := execInOK("admin", "curl -sf"+bearer+"http://127.0.0.1:8080/api/status")
		return err == nil, out
	})

	out = execIn(t, "admin", "curl -sf"+bearer+"http://127.0.0.1:8080/")
	if !strings.Contains(out, "Relay") {
		fatalf(t, "admin dashboard home is not the relay view:\n%.400s", out)
	}
	out = execIn(t, "admin", "curl -sf"+bearer+"http://127.0.0.1:8080/servers")
	// The "Enroll a Server" upload-form card was removed along with the
	// file-based enrollment flow (zero-file-enrollment task 10); "Enrolled
	// Servers" is the stable heading of the tenant table view that remains.
	if !strings.Contains(out, "Enrolled Servers") {
		fatalf(t, "admin /servers page did not render the tenant view:\n%.400s", out)
	}
	if !strings.Contains(out, "servers-search") {
		fatalf(t, "admin /servers page is missing the search/filter toolbar:\n%.400s", out)
	}

	// Live tenant table mirrors `tw relay get-servers`: server-1 up.
	serverHost := strings.TrimSpace(execIn(t, "server", "hostname"))
	out = execIn(t, "admin", "curl -sf"+bearer+"http://127.0.0.1:8080/api/servers")
	if !strings.Contains(out, serverHost) || !strings.Contains(out, `"TunnelUp":true`) {
		fatalf(t, "/api/servers does not list %s-* with TunnelUp true:\n%s", serverHost, out)
	}

	// Context store is exposed with a current context.
	out = execIn(t, "admin", "curl -sf"+bearer+"http://127.0.0.1:8080/api/config/contexts")
	if !strings.Contains(out, `"Current":true`) {
		fatalf(t, "/api/config/contexts has no current context:\n%s", out)
	}

	// Config page carries the contexts table with its search/filter toolbar.
	out = execIn(t, "admin", "curl -sf"+bearer+"http://127.0.0.1:8080/config")
	if !strings.Contains(out, "contexts-search") {
		fatalf(t, "admin /config page is missing the contexts search/filter toolbar:\n%.400s", out)
	}

	// A bogus un-enroll must be rejected on the merits (authenticated) and
	// change nothing.
	code = strings.TrimSpace(execIn(t, "admin",
		`curl -s -o /dev/null -w '%{http_code}'`+bearer+`-X POST -H 'Content-Type: application/json' -d '{"server_id":"nope-1"}' http://127.0.0.1:8080/api/servers/unenroll`))
	if code == "200" || code == "204" {
		fatalf(t, "bogus un-enroll was accepted (HTTP %s)", code)
	}
	out = execIn(t, "admin", "tw relay get-servers")
	if !strings.Contains(out, serverHost) {
		fatalf(t, "server-1 disappeared after the rejected un-enroll:\n%s", out)
	}
}
