//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// socksPort is where the suite's minimal SOCKS5 server (socks5-server,
// e2e/images/tw/socks5) listens on server2 — an otherwise-idle container at
// this point in the run, standing in for a corporate egress proxy.
const socksPort = "1080"

// testProxyRoute proves the upstream-proxy feature over the real data path:
// with `tw proxy set socks5://...` the client's whole tunnel is carried
// through the SOCKS5 hop (its log shows the relay as the CONNECT target),
// killing the hop severs connectivity entirely (traffic really depends on
// it — nothing falls back to a direct dial), and `tw proxy clear` restores
// direct dialing. Runs while alice is enrolled and must leave her connected
// directly on her default port — Revocation right after depends on that.
func testProxyRoute(t *testing.T) {
	scenario(t, "the outbound SOCKS5 proxy carries the whole tunnel, is depended on while set, and clears back to direct",
		"tw proxy shows 'not configured'; tw proxy set of an unsupported scheme is rejected",
		"tw proxy set socks5://server2:1080 persists and tw proxy echoes it",
		"a reconnected client moves real bytes AND the SOCKS5 hop's log shows a CONNECT to the relay:443",
		"with the hop killed, a fresh tw client connect never opens its tunnel port across a full 30s poll — no silent direct fallback",
		"tw proxy clear restores direct dialing and the tunnel moves bytes again")

	// Pre-state: the client (alice, connected direct since PortOverride) has
	// no proxy configured, and unsupported schemes are refused up front.
	out := execIn(t, "client", "tw proxy")
	if !strings.Contains(out, "not configured") {
		fatalf(t, "expected no proxy configured at scenario start:\n%s", out)
	}
	if out, err := execInOK("client", "tw proxy set ftp://server2:21"); err == nil {
		fatalf(t, "proxy set with an unsupported scheme unexpectedly succeeded:\n%s", out)
	} else if !strings.Contains(out, "unsupported proxy scheme") {
		fatalf(t, "expected an unsupported-scheme error, got:\n%s", out)
	}

	// Start the SOCKS5 hop on server2 (fresh log; kill any leftover first).
	killMatching(t, "server2", "socks5-server")
	execIn(t, "server2", "rm -f /shared/socks5.log")
	execDetached(t, "server2", "socks5-server -port "+socksPort+" > /shared/socks5.log 2>&1")
	waitFor(t, "socks5 hop listening on server2", 15*time.Second, func() (bool, string) {
		_, err := execInOK("server2", "nc -z 127.0.0.1 "+socksPort)
		if err != nil {
			out, _ := execInOK("server2", "cat /shared/socks5.log 2>/dev/null")
			return false, out
		}
		return true, ""
	})

	execIn(t, "client", "tw proxy set socks5://server2:"+socksPort)
	if out = execIn(t, "client", "tw proxy"); !strings.Contains(out, "socks5://server2:"+socksPort) {
		fatalf(t, "tw proxy does not echo the configured URL:\n%s", out)
	}

	// Reconnect: the tunnel must come up and move bytes THROUGH the hop.
	killMatching(t, "client", "tw client connect")
	execDetached(t, "client", "tw client connect > /var/log/tw-client-proxy.log 2>&1")
	waitFor(t, "client tunnel up through the SOCKS5 hop", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client-proxy.log")
			return false, tail
		}
		return true, ""
	})
	echoOut := execIn(t, "client", "printf 'hello-via-socks5' | nc -w 10 127.0.0.1 "+userPort)
	if strings.TrimSpace(echoOut) != "hello-via-socks5" {
		fatalf(t, "echo round-trip through the proxied tunnel mismatch: %q", echoOut)
	}
	// The hop really carried it: its log must show the relay as the target.
	socksLog := execIn(t, "server2", "cat /shared/socks5.log")
	if !strings.Contains(socksLog, ":443") ||
		!(strings.Contains(socksLog, domain) || strings.Contains(socksLog, relayIP)) {
		fatalf(t, "socks5 log shows no CONNECT to the relay on :443 — traffic did not traverse the hop:\n%s", socksLog)
	}
	t.Logf("socks5 hop log (proof the tunnel traversed it):\n%s", socksLog)

	// Dependence: kill the hop; a fresh connect must NEVER come up — proving
	// traffic actually flows through the proxy while set, with no silent
	// direct fallback. Same explicit never-answers poll as Revocation:
	// waitFor's first-failed-probe semantics would pass during the normal
	// startup window for the wrong reason.
	killMatching(t, "server2", "socks5-server")
	killMatching(t, "client", "tw client connect")
	execDetached(t, "client", "tw client connect > /var/log/tw-client-deadproxy.log 2>&1")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err == nil {
			out, _ := execInOK("client", "tail -20 /var/log/tw-client-deadproxy.log")
			fatalf(t, "tunnel port answered with the SOCKS5 hop dead — client fell back to a direct dial:\n%s", out)
		}
		time.Sleep(2 * time.Second)
	}
	tail, _ := execInOK("client", "tail -5 /var/log/tw-client-deadproxy.log")
	t.Logf("client stayed down for 30s with the hop dead, as expected; last log lines:\n%s", tail)
	killMatching(t, "client", "tw client connect")

	// Clear: back to direct. Leave alice connected on her default port.
	execIn(t, "client", "tw proxy clear")
	if out = execIn(t, "client", "tw proxy"); !strings.Contains(out, "not configured") {
		fatalf(t, "tw proxy still shows a proxy after clear:\n%s", out)
	}
	execDetached(t, "client", "tw client connect > /var/log/tw-client.log 2>&1")
	waitFor(t, "direct tunnel restored after proxy clear", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client.log")
			return false, tail
		}
		return true, ""
	})
	echoOut = execIn(t, "client", "printf 'hello-direct-again' | nc -w 10 127.0.0.1 "+userPort)
	if strings.TrimSpace(echoOut) != "hello-direct-again" {
		fatalf(t, "echo round-trip after proxy clear mismatch: %q", echoOut)
	}
}
