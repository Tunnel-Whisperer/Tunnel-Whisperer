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

// testSelfEnroll drives the single-operator shortcut: the admin machine
// enrolls ITSELF as a server tenant with one command — no join/response
// files, no context switching mid-flow — and the resulting context is born
// mode-signed and immediately usable.
func testSelfEnroll(t *testing.T) {
	scenario(t, "the admin machine self-enrolls as a server tenant in one command (single-operator flow)",
		"tw relay add-server enrolls a locally-generated identity and stores it as a new context, without touching the relay context",
		"daemon ports are de-conflicted so relay and server contexts can run side by side",
		"the new context is born mode-signed: config carries mode_auth and no 'mode is unsigned' warning appears",
		"tw server test passes from the new context (identity, mTLS admission, relay SSH auth all live)",
		"tw server start publishes the reverse tunnel: the relay LISTENs on the allocated port",
		"cleanup: un-enroll + delete-context restore the prior topology; the admin's relay tunnel still works")

	adminCtx := strings.TrimSpace(execIn(t, "admin", "tw config current-context"))

	// add-server runs EnrollServer in-process, whose step 3 re-renders the
	// relay Caddyfile — wiping the e2e local_certs shim, exactly like
	// `tw relay enroll-server` (see testServerJoin). Same dance: run
	// detached, wait for step 3's completion line, reapply the shim before
	// step 4's ~15s SSH-dial retry budget runs out.
	execIn(t, "admin", "rm -f /shared/selfenroll.log")
	execDetached(t, "admin", "tw relay add-server selfsrv > /shared/selfenroll.log 2>&1")
	waitFor(t, "add-server step 3 (Apply relay config) complete", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "cat /shared/selfenroll.log 2>/dev/null")
		return strings.Contains(out, "Caddyfile reloaded"), out
	})
	localCertsShim(t)
	waitFor(t, "add-server completion", 60*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "cat /shared/selfenroll.log 2>/dev/null")
		return strings.Contains(out, "created and enrolled"), out
	})
	log := execIn(t, "admin", "cat /shared/selfenroll.log")

	// The whole point of the single-operator flow: the born-signed profile
	// must never have printed the legacy-unsigned warning.
	if strings.Contains(log, "mode is unsigned") {
		fatalf(t, "add-server printed the unsigned-mode warning:\n%s", log)
	}
	row := regexp.MustCompile(`server-id (\S+), port (\d+)\)`).FindStringSubmatch(log)
	if row == nil {
		fatalf(t, "add-server output missing server-id/port:\n%s", log)
	}
	selfID, selfPort := row[1], row[2]
	// The admin profile holds the default daemon ports, so the new context
	// must have been moved off them.
	if !strings.Contains(log, "Daemon ports adjusted") {
		fatalf(t, "add-server did not de-conflict daemon ports:\n%s", log)
	}

	// The relay context is untouched and still fully working.
	if cur := strings.TrimSpace(execIn(t, "admin", "tw config current-context")); cur != adminCtx {
		fatalf(t, "add-server changed the current context to %q (was %q)", cur, adminCtx)
	}
	out := execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin tunnel broken after add-server:\n%s", out)
	}

	// Switch into the new context: it must be signed and immediately usable.
	execIn(t, "admin", "tw config use-context selfsrv")
	if viewOut := execIn(t, "admin", "tw config view"); !strings.Contains(viewOut, "mode_auth:") {
		fatalf(t, "self-enrolled profile is not mode-signed (no mode_auth: block):\n%s", viewOut)
	}
	testOut := execIn(t, "admin", "tw server test 2>&1")
	if !strings.Contains(testOut, "tunnel and shell working") {
		fatalf(t, "self-enrolled server test failed:\n%s", testOut)
	}
	if strings.Contains(testOut, "mode is unsigned") {
		fatalf(t, "self-enrolled context still warns 'mode is unsigned':\n%s", testOut)
	}

	// The daemon publishes the reverse tunnel on the allocated relay port.
	execDetached(t, "admin", "tw server start > /var/log/tw-selfsrv.log 2>&1")
	portN, err := strconv.Atoi(selfPort)
	if err != nil {
		fatalf(t, "unparseable port %q from add-server output", selfPort)
	}
	listenRe := regexp.MustCompile(fmt.Sprintf(`(?m)^\s*\d+: [0-9A-F]+:%04X\s+\S+\s+0A\b`, portN))
	waitFor(t, "relay LISTEN on the self-enrolled tenant's port", 120*time.Second, func() (bool, string) {
		proc, _ := execInOK("relay", "cat /proc/net/tcp /proc/net/tcp6")
		if listenRe.MatchString(proc) {
			return true, ""
		}
		srvLog, _ := execInOK("admin", "tail -n 20 /var/log/tw-selfsrv.log 2>/dev/null")
		return false, srvLog
	})

	// Cleanup: stop the daemon, switch back, remove the tenant + context, and
	// prove the topology is unaffected for the scenarios that follow.
	killMatching(t, "admin", "tw server start")
	execIn(t, "admin", "tw config use-context "+adminCtx)
	out = execIn(t, "admin", "tw relay un-enroll-server "+selfID+" --yes")
	if !strings.Contains(out, "Un-enrolled "+selfID) {
		fatalf(t, "un-enroll of the self-enrolled tenant did not report success:\n%s", out)
	}
	// The un-enroll re-rendered the Caddyfile — reapply the shim again.
	localCertsShim(t)
	execIn(t, "admin", "tw config delete-context selfsrv")
	regOut := execIn(t, "admin", "tw relay get-servers")
	if strings.Contains(regOut, selfID) {
		fatalf(t, "get-servers still lists %s after un-enroll:\n%s", selfID, regOut)
	}
	out = execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin tunnel broken after self-enroll cleanup:\n%s", out)
	}
}
