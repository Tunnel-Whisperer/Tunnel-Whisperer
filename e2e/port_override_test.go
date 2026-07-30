//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// Ports for this scenario only — distinct from alice's 18080 (userPort) and
// bob's 18081 (RelayResilience).
const (
	overridePort = "18090" // persisted via tw client set-port
	mapPort      = "18091" // one-shot via tw client connect --map
)

// testPortOverride proves a client can remap the admin-chosen local port:
// conflict fails fast with an actionable error, set-port persists a remap,
// --map does a one-shot remap, --clear restores the default. Runs between
// UserLifecycle (alice exists + connected) and PermitOpen (needs alice
// connected on her default port when this scenario ends).
func testPortOverride(t *testing.T) {
	scenario(t, "a client remaps an admin-chosen local port that conflicts on their machine",
		"with alice's local port occupied, tw client connect fails fast with the set-port/--map hint (not a silent background failure)",
		"tw client set-port persists an override; reconnect moves real bytes through the new local port",
		"tw client connect --map remaps for one run only — the persisted config is untouched",
		"tw client set-port --clear restores the admin default (second --clear reports no override)")

	// Take down alice's live tunnel and occupy her local port.
	killMatching(t, "client", "tw client connect")
	execDetached(t, "client", "nc -lk 127.0.0.1 "+userPort)

	// Fail fast: preflight must reject the start with the actionable error.
	out, err := execInOK("client", "timeout 30 tw client connect 2>&1")
	if err == nil {
		fatalf(t, "tw client connect succeeded despite the occupied port:\n%s", out)
	}
	if !strings.Contains(out, "already in use") || !strings.Contains(out, "tw client set-port "+echoPort) {
		fatalf(t, "conflict error is not the actionable preflight message:\n%s", out)
	}

	// Persisted override: alice's server port (echoPort) → overridePort.
	execIn(t, "client", "tw client set-port "+echoPort+" "+overridePort)
	listOut := execIn(t, "client", "tw client set-port")
	if !strings.Contains(listOut, overridePort) {
		fatalf(t, "set-port listing does not show the override:\n%s", listOut)
	}
	execDetached(t, "client", "tw client connect > /var/log/tw-client-override.log 2>&1")
	waitFor(t, "overridden tunnel listening", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+overridePort); err != nil {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client-override.log")
			return false, tail
		}
		return true, ""
	})
	echoOut := execIn(t, "client", "printf 'hello-override' | nc -w 10 127.0.0.1 "+overridePort)
	if strings.TrimSpace(echoOut) != "hello-override" {
		fatalf(t, "echo through overridden port mismatch: %q", echoOut)
	}
	// The default port is still held by our occupier, not by tw.
	killMatching(t, "client", "tw client connect")

	// One-shot --map: wins over the persisted override, not persisted.
	execDetached(t, "client", "tw client connect --map "+mapPort+":"+echoPort+" > /var/log/tw-client-map.log 2>&1")
	waitFor(t, "one-shot mapped tunnel listening", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+mapPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client-map.log")
			return false, tail
		}
		return true, ""
	})
	echoOut = execIn(t, "client", "printf 'hello-map' | nc -w 10 127.0.0.1 "+mapPort)
	if strings.TrimSpace(echoOut) != "hello-map" {
		fatalf(t, "echo through --map port mismatch: %q", echoOut)
	}
	cfgOut := execIn(t, "client", "tw config view")
	if strings.Contains(cfgOut, mapPort) {
		fatalf(t, "--map port leaked into persisted config:\n%s", cfgOut)
	}
	if !strings.Contains(cfgOut, overridePort) {
		fatalf(t, "persisted set-port override missing from config:\n%s", cfgOut)
	}
	killMatching(t, "client", "tw client connect")

	// --clear restores the default. Free the occupier first so the default
	// port is bindable again.
	killMatching(t, "client", "nc -lk")
	execIn(t, "client", "tw client set-port "+echoPort+" --clear")
	clearOut := execIn(t, "client", "tw client set-port "+echoPort+" --clear")
	if !strings.Contains(clearOut, "no override") {
		fatalf(t, "second --clear should report no override:\n%s", clearOut)
	}

	// Leave alice connected on her DEFAULT port — PermitOpen depends on it.
	execDetached(t, "client", "tw client connect > /var/log/tw-client.log 2>&1")
	waitFor(t, "default tunnel restored", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err != nil {
			tail, _ := execInOK("client", "tail -5 /var/log/tw-client.log")
			return false, tail
		}
		return true, ""
	})
}
