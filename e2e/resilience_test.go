//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// bobPort is RelayResilience's client-side tunnel port for user bob — distinct
// from UserLifecycle's alice port so a stale listener from an earlier scenario
// can never satisfy this scenario's probes.
const bobPort = "18081"

// testRelayResilience proves the tunnel self-heals after the relay's caddy and
// xray are restarted: every live VLESS/TLS session is severed, and both the
// server's reverse tunnel and the client's forward tunnel must reconnect on
// their own — no tw process is restarted anywhere.
func testRelayResilience(t *testing.T) {
	scenario(t, "the tunnel self-heals after the relay's caddy and xray are restarted",
		"a fresh user (bob) is created, shipped to the client, and moves real bytes through the tunnel (baseline)",
		"systemctl restart xray caddy on the relay severs every live session (new MainPIDs prove the restart happened)",
		"the server's reverse tunnel reconnects on its own — tw server test passes again with no tw restart",
		"the client's forward tunnel reconnects on its own — the same byte-for-byte echo round-trip succeeds again",
		"the admin's tw relay test still passes after the restart")

	// Re-runnability: a prior run may have left a live client connect and a
	// leftover bob on the server. Alice is gone since Revocation, so bob is
	// this scenario's own user; wipe defensively before creating him.
	killMatching(t, "client", "tw client connect")
	if out, err := execInOK("server", "printf 'y\\n' | tw server user delete bob"); err != nil {
		t.Logf("pre-cleanup: no leftover bob on the server (or delete failed, non-fatal): %v\n%s", err, out)
	} else {
		t.Logf("pre-cleanup: deleted a leftover bob from a previous run:\n%s", out)
	}

	// Baseline: bob works end-to-end before the disruption, so a recovery
	// failure below can only be blamed on the restart.
	execIn(t, "server", "tw server user create bob -m "+bobPort+":"+echoPort)
	execIn(t, "server", "tw server user apply bob")
	execIn(t, "server", "cd /shared && rm -f bob-tw-context.twctx && tw config export-user bob")
	execIn(t, "client", "tw config import /shared/bob-tw-context.twctx --activate")
	execDetached(t, "client", "tw client connect > /var/log/tw-client-bob.log 2>&1")
	waitFor(t, "bob tunnel listening", 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+bobPort); err != nil {
			out, _ := execInOK("client", "tail -5 /var/log/tw-client-bob.log")
			return false, out
		}
		return true, ""
	})
	echoOut := execIn(t, "client", "printf 'hello-tw-baseline' | nc -w 10 127.0.0.1 "+bobPort)
	if strings.TrimSpace(echoOut) != "hello-tw-baseline" {
		fatalf(t, "baseline echo round-trip mismatch: %q", echoOut)
	}

	// The disruption. Capture the units' MainPIDs around the restart so the
	// recovery below can't pass because the restart silently didn't happen.
	pidsBefore := execIn(t, "relay", "systemctl show -p MainPID xray caddy")
	execIn(t, "relay", "systemctl restart xray caddy")
	pidsAfter := execIn(t, "relay", "systemctl show -p MainPID xray caddy")
	if strings.TrimSpace(pidsBefore) == strings.TrimSpace(pidsAfter) {
		fatalf(t, "relay xray/caddy MainPIDs unchanged across systemctl restart:\nbefore: %s\nafter: %s", pidsBefore, pidsAfter)
	}
	for _, unit := range []string{"caddy", "xray"} {
		execIn(t, "relay", "systemctl is-active "+unit)
	}
	t.Logf("relay xray+caddy restarted (MainPIDs before/after):\n%s%s", pidsBefore, pidsAfter)

	// Recovery — nothing is restarted on the tw side; the daemons' own
	// reconnect loops must bring both tunnels back.
	waitFor(t, "server reverse tunnel recovery", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})
	waitFor(t, "client echo round-trip recovery", 120*time.Second, func() (bool, string) {
		out, err := execInOK("client", "printf 'hello-tw-resilience' | nc -w 5 127.0.0.1 "+bobPort+" 2>&1")
		if err != nil || strings.TrimSpace(out) != "hello-tw-resilience" {
			logOut, _ := execInOK("client", "tail -5 /var/log/tw-client-bob.log")
			return false, "echo not yet recovered: " + strings.TrimSpace(out) + "\n" + logOut
		}
		return true, ""
	})
	out := execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin tunnel broken after relay service restart:\n%s", out)
	}
}

// testTeardown destroys the manual relay from the admin profile and proves
// the status reflects it. For a manual relay, destroy is config-side by
// contract (DestroyRelay in internal/ops/relay.go): it removes the relay dir
// (marker included) and deactivates users — the VM itself is the admin's to
// decommission. Runs last: after this the admin profile has no relay, and
// only a fresh RelayInstall (which re-creates from scratch) can follow.
func testTeardown(t *testing.T) {
	scenario(t, "tw relay destroy removes the manual relay and tw relay status reflects it",
		"pre-destroy, tw relay status reports Provisioned: yes with the relay's domain",
		"tw relay destroy (confirmed over stdin) completes with 'Relay destroyed.'",
		"the relay dir (manual-relay.json marker included) is removed from the admin profile",
		"relay_host is cleared from config, so the marker-less status fallback cannot resurrect the destroyed relay",
		"tw relay status now reports Provisioned: no",
		"a second destroy is a clean no-op: 'No relay is currently provisioned.'")

	out := execIn(t, "admin", "tw relay status")
	if !strings.Contains(out, "Provisioned: yes") || !strings.Contains(out, domain) {
		fatalf(t, "pre-destroy relay status does not report the provisioned relay:\n%s", out)
	}

	// The [y/N] confirmation prompt reads stdin — no TTY here, so pipe the answer.
	out = execIn(t, "admin", "printf 'y\\n' | tw relay destroy")
	if !strings.Contains(out, "Relay destroyed.") {
		fatalf(t, "destroy did not report success:\n%s", out)
	}

	// The whole relay dir is gone (TW_CONFIG_DIR=/etc/tw-test in the e2e
	// images, so config.RelayDir() is /etc/tw-test/relay).
	execIn(t, "admin", "test ! -e /etc/tw-test/relay")

	// The config no longer points at the relay (GetRelayStatus's marker-less
	// fallback would otherwise resurrect it from relay_host + client cert).
	if viewOut := execIn(t, "admin", "tw config view"); strings.Contains(viewOut, "relay_host: "+domain) {
		fatalf(t, "relay_host still set in config after destroy:\n%s", viewOut)
	}

	out = execIn(t, "admin", "tw relay status")
	if !strings.Contains(out, "Provisioned: no") {
		fatalf(t, "post-destroy relay status still reports a relay:\n%s", out)
	}

	// Destroying again must be a no-op, not an error.
	out = execIn(t, "admin", "printf 'y\\n' | tw relay destroy")
	if !strings.Contains(out, "No relay is currently provisioned.") {
		fatalf(t, "second destroy is not a clean no-op:\n%s", out)
	}

	// Leave the kept topology quiet: stop bob's client from reconnect-spamming
	// (the relay VM itself is untouched by a manual destroy, by design).
	killMatching(t, "client", "tw client connect")
}
