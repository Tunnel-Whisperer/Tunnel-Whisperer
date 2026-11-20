//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// socksPort is where the suite's minimal SOCKS5 server (socks5-server,
// e2e/images/tw/socks5) listens on server2 — an otherwise-idle container at
// this point in the run, standing in for a corporate egress proxy.
const socksPort = "1080"

// connect443RE matches a hop log line "CONNECT <host>:443 from <addr>" but not
// :4430 or a source port that merely starts with 443.
var connect443RE = regexp.MustCompile(`(?m)CONNECT \S+:443(\D|$)`)

// countConnect443 counts the CONNECTs to port 443 in a SOCKS5 hop log.
func countConnect443(log string) int {
	return len(connect443RE.FindAllStringIndex(log, -1))
}

// testProxyRoute proves the upstream-proxy feature over the real data path:
// with `tw proxy set socks5://...` the client's whole tunnel is carried
// through the SOCKS5 hop (its log shows the relay as the CONNECT target),
// killing the hop severs connectivity entirely (traffic really depends on
// it — nothing falls back to a direct dial), and `tw proxy clear` restores
// direct dialing. Runs while alice is enrolled and must leave her connected
// directly on her default port — Revocation right after depends on that.
func testProxyRoute(t *testing.T) {
	scenario(t, "the outbound SOCKS5 proxy carries the whole tunnel, is depended on while set, and clears back to direct",
		"tw proxy shows 'Proxy: none'; tw proxy set of an unsupported scheme is rejected",
		"tw proxy set socks5://server2:1080 persists and tw proxy echoes it",
		"a reconnected client moves real bytes AND the SOCKS5 hop's log shows a CONNECT to the relay:443",
		"with the hop killed, a fresh tw client connect never opens its tunnel port across a full 30s poll — no silent direct fallback",
		"tw proxy clear restores direct dialing and the tunnel moves bytes again")

	// Pre-state: the client (alice, connected direct since PortOverride) has
	// no proxy configured, and unsupported schemes are refused up front.
	out := execIn(t, "client", "tw proxy")
	if !strings.Contains(out, "Proxy: none") {
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
	if countConnect443(socksLog) == 0 ||
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
	if out = execIn(t, "client", "tw proxy"); !strings.Contains(out, "Proxy: none") {
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

// testProxyEnv proves the env-only path: with no config proxy, HTTPS_PROXY
// alone routes the client tunnel and the admin's relay HTTPS check through
// the SOCKS5 hop, an invalid env value is a hard error, and NO_PROXY for the
// relay host makes the tunnel dial direct. Runs right after ProxyRoute (proxy cleared, alice
// connected direct) and must leave her connected directly.
func testProxyEnv(t *testing.T) {
	scenario(t, "HTTPS_PROXY alone (no config proxy) carries the tunnel and the relay HTTPS check through the SOCKS5 hop",
		"tw proxy reports 'Proxy: none' with no config and no env, and '(source: env HTTPS_PROXY)' with the env var set",
		"NO_PROXY for the relay host does not change what tw proxy resolves, but a client reconnected with it dials the relay directly (no new hop CONNECT); an unsupported env scheme fails naming HTTPS_PROXY",
		"tw client connect with HTTPS_PROXY moves real bytes, the hop log shows a CONNECT :443, and the daemon logs the outbound proxy",
		"tw relay test on admin with HTTPS_PROXY exits 0 and adds two CONNECT :443 (HTTPS step + Xray step) to the hop log",
		"without the env var the client dials direct again and moves bytes")

	const envProxy = "HTTPS_PROXY=socks5://server2:" + socksPort

	out := execIn(t, "client", "tw proxy")
	if !strings.Contains(out, "Proxy: none") {
		fatalf(t, "expected no proxy at scenario start:\n%s", out)
	}

	killMatching(t, "server2", "socks5-server")
	execIn(t, "server2", "rm -f /shared/socks5-env.log")
	execDetached(t, "server2", "socks5-server -port "+socksPort+" > /shared/socks5-env.log 2>&1")
	waitFor(t, "socks5 hop listening on server2", 15*time.Second, func() (bool, string) {
		_, err := execInOK("server2", "nc -z 127.0.0.1 "+socksPort)
		if err != nil {
			out, _ := execInOK("server2", "cat /shared/socks5-env.log 2>/dev/null")
			return false, out
		}
		return true, ""
	})

	out = execIn(t, "client", envProxy+" tw proxy")
	if !strings.Contains(out, "socks5://server2:"+socksPort) || !strings.Contains(out, "(source: env HTTPS_PROXY)") {
		fatalf(t, "tw proxy does not report the env proxy:\n%s", out)
	}
	out = execIn(t, "client", envProxy+" NO_PROXY="+domain+" tw proxy")
	if !strings.Contains(out, "(source: env HTTPS_PROXY)") {
		fatalf(t, "NO_PROXY for the relay changed the resolved env proxy (resolution must be per target):\n%s", out)
	}
	if out, err := execInOK("client", "HTTPS_PROXY=ftp://bad tw proxy"); err == nil {
		fatalf(t, "tw proxy with an unsupported env scheme unexpectedly succeeded:\n%s", out)
	} else if !strings.Contains(out, "HTTPS_PROXY") || !strings.Contains(out, "unsupported proxy scheme") {
		fatalf(t, "expected an HTTPS_PROXY unsupported-scheme error, got:\n%s", out)
	}

	// NO_PROXY for the relay host: the tunnel must dial the relay directly,
	// so the hop sees no new :443 CONNECT while the echo still works.
	killMatching(t, "client", "tw client connect")
	noProxyBefore := countConnect443(execIn(t, "server2", "cat /shared/socks5-env.log"))
	execIn(t, "client", "rm -f /shared/client-noproxy.log")
	execDetached(t, "client", envProxy+" NO_PROXY="+domain+" tw client connect > /shared/client-noproxy.log 2>&1")
	waitFor(t, "client tunnel up directly with NO_PROXY for the relay", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /shared/client-noproxy.log")
			return false, tail
		}
		return true, ""
	})
	echoOut := execIn(t, "client", "printf 'hello-noproxy' | nc -w 10 127.0.0.1 "+userPort)
	if strings.TrimSpace(echoOut) != "hello-noproxy" {
		fatalf(t, "echo round-trip with NO_PROXY mismatch: %q", echoOut)
	}
	socksLog := execIn(t, "server2", "cat /shared/socks5-env.log")
	if got := countConnect443(socksLog); got != noProxyBefore {
		fatalf(t, "NO_PROXY for the relay did not bypass the hop (before=%d after=%d):\n%s", noProxyBefore, got, socksLog)
	}

	// Reconnect with only the env var: the tunnel must traverse the hop.
	killMatching(t, "client", "tw client connect")
	execIn(t, "client", "rm -f /shared/client-env.log")
	execDetached(t, "client", envProxy+" tw client connect > /shared/client-env.log 2>&1")
	waitFor(t, "client tunnel up through the env SOCKS5 hop", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /shared/client-env.log")
			return false, tail
		}
		return true, ""
	})
	echoOut = execIn(t, "client", "printf 'hello-via-env' | nc -w 10 127.0.0.1 "+userPort)
	if strings.TrimSpace(echoOut) != "hello-via-env" {
		fatalf(t, "echo round-trip through the env-proxied tunnel mismatch: %q", echoOut)
	}
	socksLog = execIn(t, "server2", "cat /shared/socks5-env.log")
	if countConnect443(socksLog) == 0 {
		fatalf(t, "socks5 log shows no CONNECT :443 — env proxy did not carry the tunnel:\n%s", socksLog)
	}
	clientLog := execIn(t, "client", "cat /shared/client-env.log")
	if !strings.Contains(clientLog, "outbound proxy") || !strings.Contains(clientLog, "source: env HTTPS_PROXY") {
		fatalf(t, "client log does not announce the env outbound proxy:\n%s", clientLog)
	}
	t.Logf("client log:\n%s", clientLog)
	before := countConnect443(socksLog)

	// The admin's relay HTTPS health-check step goes through the env proxy too.
	if out, err := execInOK("admin", envProxy+" tw relay test"); err != nil {
		fatalf(t, "tw relay test with HTTPS_PROXY failed: %v\n%s", err, out)
	}
	socksLog = execIn(t, "server2", "cat /shared/socks5-env.log")
	if after := countConnect443(socksLog); after < before+2 {
		fatalf(t, "tw relay test did not add two CONNECT :443 (HTTPS + Xray steps) to the hop log (before=%d after=%d):\n%s", before, after, socksLog)
	}
	t.Logf("socks5 hop log:\n%s", socksLog)

	// Back to direct. Leave alice connected on her default port.
	killMatching(t, "server2", "socks5-server")
	killMatching(t, "client", "tw client connect")
	execDetached(t, "client", "tw client connect > /var/log/tw-client.log 2>&1")
	waitFor(t, "direct tunnel restored without the env var", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client.log")
			return false, tail
		}
		return true, ""
	})
	echoOut = execIn(t, "client", "printf 'hello-direct-env' | nc -w 10 127.0.0.1 "+userPort)
	if strings.TrimSpace(echoOut) != "hello-direct-env" {
		fatalf(t, "echo round-trip after dropping the env proxy mismatch: %q", echoOut)
	}
}
