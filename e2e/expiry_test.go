//go:build e2e

package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const evePort = "18096"

// testUserExpiry invites eve with a 2-minute credential, proves her live
// session is cut off by the server's expiry timer (nothing is deleted), and
// that she cannot reconnect. Uses throwaway --config-dirs on the client so
// the main profile is untouched. Takes ~3 minutes: the daemon rescans users
// every 60s, so the timer is armed within a minute and fires at T+2m.
func testUserExpiry(t *testing.T) {
	scenario(t, "a user invited with --expires stops working at expiry even though nothing was deleted",
		"tw server user invite eve -m <evePort>:<echoPort> --expires 2m issues a cert whose NotAfter is ~2 minutes out (openssl x509 -enddate on the client's cert) and prints 'Credential expires:'",
		"eve's tw client connect forwards echo before expiry",
		"at expiry the server logs 'user credential expired, sessions disconnected' for eve and her live session stops forwarding (no echo across a 20s poll)",
		"a fresh tw client connect for eve never opens its tunnel port within 30s and never reaches the server's SSH layer (no new 'expired user refused' line), unlike her live client which was refused there (consistent with the relay refusing the expired cert at the TLS handshake)",
		"tw server user list shows 'Expires: EXPIRED' for eve",
		"re-inviting eve errors with 'has expired; delete it first', and after tw server user delete eve a new invite for eve works")

	cleanup := func() {
		killMatching(t, "client", "twexp")
		execIn(t, "client", "rm -rf /tmp/twexp /tmp/twexp2")
		for _, c := range []string{"unregister", "delete"} {
			if out, err := execInOK("server", "printf 'y\\n' | tw server user "+c+" eve"); err != nil {
				t.Logf("cleanup: tw server user %s eve failed (expected if already gone): %v\n%s", c, err, out)
			}
		}
	}
	cleanup()
	defer cleanup()

	issuerLog, joinLog := runInviteExchange(t, "server", "tw server user invite eve -m "+evePort+":"+echoPort+" --expires 2m",
		"client", "tw --config-dir /tmp/twexp join "+domain+" {code}")
	if !regexp.MustCompile(`Credential expires: \d{4}-\d\d-\d\dT`).MatchString(issuerLog) {
		fatalf(t, "invite did not print 'Credential expires: <RFC3339>':\n%s", issuerLog)
	}
	if !strings.Contains(joinLog, `Context "eve" (client) created.`) {
		fatalf(t, "join log missing the context-created line:\n%s", joinLog)
	}

	// The enrolled cert's NotAfter is ~2 minutes out; invite + join take well
	// under a minute of that, so at least 60s must remain.
	remOut := execIn(t, "client", `f=$(find /tmp/twexp -name client.crt | head -1); test -n "$f" && `+
		`e=$(openssl x509 -enddate -noout -in "$f" | cut -d= -f2) && echo $(( $(date -d "$e" +%s) - $(date +%s) ))`)
	rem, err := strconv.Atoi(strings.TrimSpace(remOut))
	if err != nil || rem < 60 || rem > 120 {
		fatalf(t, "client cert NotAfter is %q seconds away, want within [60, 120]", remOut)
	}
	t.Logf("eve's client cert expires in %ds", rem)

	execDetached(t, "client", "tw --config-dir /tmp/twexp client connect > /var/log/tw-client-eve.log 2>&1")
	echo := "printf 'hello-tw-expiry' | nc -w 5 127.0.0.1 " + evePort + " 2>&1"
	waitFor(t, "eve's tunnel forwards before expiry", 120*time.Second, func() (bool, string) {
		out, err := execInOK("client", echo)
		if err != nil || strings.TrimSpace(out) != "hello-tw-expiry" {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client-eve.log")
			return false, out + "\n" + tail
		}
		return true, ""
	})

	// The cut-off: logged by the server's expiry timer, not by any delete.
	const expiredLog = "grep -c 'user credential expired, sessions disconnected.*user=eve' /var/log/tw-server.log || true"
	expiredBefore, err := strconv.Atoi(strings.TrimSpace(execIn(t, "server", expiredLog)))
	if err != nil {
		fatalf(t, "counting expiry log lines: %v", err)
	}
	waitFor(t, "server logs eve's credential expiry", 150*time.Second, func() (bool, string) {
		out, _ := execInOK("server", expiredLog)
		n, err := strconv.Atoi(strings.TrimSpace(out))
		return err == nil && n > expiredBefore, out
	})
	const refusedLog = "grep -c 'expired user refused.*user=eve' /var/log/tw-server.log || true"
	liveRefusedBefore, err := strconv.Atoi(strings.TrimSpace(execIn(t, "server", refusedLog)))
	if err != nil {
		fatalf(t, "counting refusal log lines: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if out, err := execInOK("client", echo); err == nil && strings.TrimSpace(out) == "hello-tw-expiry" {
			tail, _ := execInOK("client", "tail -20 /var/log/tw-client-eve.log")
			srvLog, _ := execInOK("server", "date -u; grep 'user=eve' /var/log/tw-server.log | tail -10")
			fatalf(t, "eve's session still forwards after her credential expired:\n%s\n--- server ---\n%s", tail, srvLog)
		}
		time.Sleep(2 * time.Second)
	}

	liveRefusedAfter, err := strconv.Atoi(strings.TrimSpace(execIn(t, "server", refusedLog)))
	if err != nil || liveRefusedAfter <= liveRefusedBefore {
		srvLog, _ := execInOK("server", "tail -20 /var/log/tw-server.log")
		fatalf(t, "eve's live client was not refused at the SSH layer during the 20s poll (expired-user refusals %d before, %d after, err %v):\n%s", liveRefusedBefore, liveRefusedAfter, err, srvLog)
	}

	// A fresh connect never comes up. The client log carries only generic
	// "SSH handshake failed: EOF" lines and no TLS error is logged anywhere,
	// so there is no stable TLS string to assert. What is proven: the live
	// client was refused at the SSH layer (counter grew above), while the
	// fresh client adds no such line, i.e. it never reached the server's SSH
	// (consistent with the relay refusing the expired cert at the TLS
	// handshake).
	killMatching(t, "client", "twexp")
	time.Sleep(5 * time.Second) // let the killed client's last in-flight retry land
	refusedBefore, err := strconv.Atoi(strings.TrimSpace(execIn(t, "server", refusedLog)))
	if err != nil {
		fatalf(t, "counting refusal log lines: %v", err)
	}
	execDetached(t, "client", "tw --config-dir /tmp/twexp client connect > /var/log/tw-client-eve-expired.log 2>&1")
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+evePort); err == nil {
			tail, _ := execInOK("client", "tail -20 /var/log/tw-client-eve-expired.log")
			fatalf(t, "expired client's tunnel port answered:\n%s", tail)
		}
		time.Sleep(2 * time.Second)
	}
	tail, _ := execInOK("client", "tail -10 /var/log/tw-client-eve-expired.log")
	t.Logf("expired client stayed down for 30s; client log tail:\n%s", tail)
	refusedAfter := strings.TrimSpace(execIn(t, "server", refusedLog))
	if n, err := strconv.Atoi(refusedAfter); err != nil || n != refusedBefore {
		srvLog, _ := execInOK("server", "tail -20 /var/log/tw-server.log")
		fatalf(t, "eve's fresh connect reached the server's SSH layer (expired-user refusals %d before, %q after) — only the live client should reach it:\n%s", refusedBefore, refusedAfter, srvLog)
	}
	killMatching(t, "client", "twexp")

	listOut := execIn(t, "server", "tw server user list")
	if !regexp.MustCompile(`(?m)^  eve\n(?:    .*\n)*?    Expires: EXPIRED`).MatchString(listOut) {
		fatalf(t, "user list does not show Expires: EXPIRED for eve:\n%s", listOut)
	}

	// An expired name cannot be re-invited until it is deleted.
	reOut, reErr := execInOK("server", "timeout 20 tw server user invite eve -m "+evePort+":"+echoPort)
	if reErr == nil || !strings.Contains(reOut, "has expired; delete it first") {
		fatalf(t, "re-invite of expired eve: err=%v, want 'has expired; delete it first':\n%s", reErr, reOut)
	}
	execIn(t, "server", "printf 'y\\n' | tw server user unregister eve")
	execIn(t, "server", "printf 'y\\n' | tw server user delete eve")
	_, joinLog = runInviteExchange(t, "server", "tw server user invite eve -m "+evePort+":"+echoPort,
		"client", "tw --config-dir /tmp/twexp2 join "+domain+" {code}")
	if !strings.Contains(joinLog, `Context "eve" (client) created.`) {
		fatalf(t, "re-invite of eve after delete did not enroll:\n%s", joinLog)
	}
}
