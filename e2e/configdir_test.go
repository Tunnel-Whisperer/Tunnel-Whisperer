//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// carolPort is ConfigDirSafety's invite port mapping — distinct from every
// other scenario's port (echoPort itself, alice's userPort, bob's bobPort,
// PortOverride's 18090/18091, permitOpenProbePort). Never actually bound: this
// scenario never runs `tw client connect`, only `tw join` and `tw client
// test`, so the value just needs to be a syntactically valid, unused port.
const carolPort = "18093"

// testConfigDirSafety proves two fixes end-to-end, on the client container,
// entirely inside its own --config-dir so the client's main profile (bob's,
// live since RelayResilience) is never touched:
//
//   A) write-permission preflight (bf9da7d): `tw join` against an unwritable
//      config dir fails locally BEFORE any network call, so the issuer's
//      one-time invite is never burned — the same code can still be redeemed
//      afterwards.
//   B) relative config-dir resolution (1ee3192): `tw --config-dir <relative>`
//      works end-to-end, including the xray-core client-cert path. The
//      reported bug was `client test` step 3 failing with
//      `open /usr/local/bin/<rel>/client.crt` because xray-core resolves
//      relative paths against its own executable's directory, not the
//      process's cwd.
func testConfigDirSafety(t *testing.T) {
	scenario(t, "an unwritable config dir fails tw join before any network call (invite not burned), and a RELATIVE --config-dir works end-to-end including the xray client-cert path",
		"a fresh user (carol) is invited on the server",
		"tw --config-dir /tmp/twbadcfg join fails locally with a 'not writable' error, and the issuer log shows no SAS exchange yet — nothing reached the network",
		"the SAME invite code still works afterwards: tw --config-dir ./twcarol join (a RELATIVE path, run from /tmp/carolwork) completes the SAS dance and creates the client context",
		"tw --config-dir ./twcarol client test (same relative flag, same cwd) fully succeeds and never mentions /usr/local/bin — proving xray-core loaded the client cert via the absolutized path",
		"the client's main profile (bob's, from RelayResilience) is untouched throughout")

	beforeCtx := execIn(t, "client", "tw config current-context")

	// Re-runnability: wipe any leftovers from a prior partial run before
	// creating anything new.
	killMatching(t, "client", "twcarol")
	execIn(t, "client", "rm -rf /tmp/carolwork /tmp/twbadcfg")
	if out, err := execInOK("server", "printf 'y\\n' | tw server user delete carol"); err != nil {
		t.Logf("pre-cleanup: no leftover carol on the server (or delete failed, non-fatal): %v\n%s", err, out)
	} else {
		t.Logf("pre-cleanup: deleted a leftover carol from a previous run:\n%s", out)
	}

	// Mint carol's invite the same way runInviteExchange does (see
	// harness.go for the FIFO/SAS/sentinel rationale), but split around the
	// failed-join step in between: the issuer must sit at its SAS prompt
	// across BOTH the failed preflight attempt and the real join that
	// follows, since both use the same code.
	const (
		fifo      = "/tmp/tw-approve-configdir"
		issuerLog = "/shared/configdir-issuer.log"
		joinLog   = "/shared/configdir-join.log"
	)
	execIn(t, "server", "rm -f "+fifo+" "+issuerLog)
	issuerScript := "mkfifo -m 600 " + fifo + " && exec 9<>" + fifo + " && (" +
		"tw server user invite carol -m " + carolPort + ":" + echoPort + " <&9 > " + issuerLog + " 2>&1; " +
		"echo \"TW_INVITE_EXIT $?\" >> " + issuerLog + ")"
	execDetached(t, "server", issuerScript)

	var code string
	waitFor(t, "carol invite code minted", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("server", "cat "+issuerLog+" 2>/dev/null")
		if m := inviteCodeRe.FindStringSubmatch(out); m != nil {
			code = m[1]
			return true, ""
		}
		return false, out
	})

	// --- A: the write-permission preflight must fail BEFORE any network call ---
	//
	// A directory occupying the probe filename makes CheckWritable's
	// os.Create fail EISDIR even as root (root can't open() a directory for
	// writing either) — the same trick config_test.go uses at the unit level.
	execIn(t, "client", "mkdir -p /tmp/twbadcfg/.tw-write-probe")
	badOut, badErr := execInOK("client", "tw --config-dir /tmp/twbadcfg join "+domain+" "+code)
	if badErr == nil {
		fatalf(t, "tw join against an unwritable config dir exited 0 (expected a failure):\n%s", badOut)
	}
	if !strings.Contains(badOut, "not writable") {
		fatalf(t, "tw join against an unwritable config dir did not report 'not writable':\n%s", badOut)
	}
	issuerAfterBad, _ := execInOK("server", "cat "+issuerLog+" 2>/dev/null")
	if strings.Contains(issuerAfterBad, "SAS:") {
		fatalf(t, "issuer already printed a SAS after the LOCAL preflight failure — the failed join reached the network, so the invite may have been burned:\n%s", issuerAfterBad)
	}
	if inviteExitRe.MatchString(issuerAfterBad) {
		fatalf(t, "issuer command already exited after the preflight-failed join attempt — it should still be waiting at its SAS prompt:\n%s", issuerAfterBad)
	}

	// --- B: the SAME code, redeemed via a RELATIVE --config-dir ---
	execIn(t, "client", "mkdir -p /tmp/carolwork && rm -f "+joinLog)
	execDetached(t, "client", "cd /tmp/carolwork && tw --config-dir ./twcarol join "+domain+" "+code+" > "+joinLog+" 2>&1")

	var issuerSAS, joinSAS string
	waitFor(t, "both SAS strings shown (configdir join)", 60*time.Second, func() (bool, string) {
		iOut, _ := execInOK("server", "cat "+issuerLog+" 2>/dev/null")
		jOut, _ := execInOK("client", "cat "+joinLog+" 2>/dev/null")
		im, jm := inviteSASRe.FindStringSubmatch(iOut), inviteSASRe.FindStringSubmatch(jOut)
		if im == nil || jm == nil {
			return false, iOut + "\n---\n" + jOut
		}
		issuerSAS, joinSAS = im[1], jm[1]
		return true, ""
	})
	if issuerSAS != joinSAS {
		fatalf(t, "SAS mismatch: issuer %q vs enrollee %q — MITM or protocol bug", issuerSAS, joinSAS)
	}
	execIn(t, "server", "echo y > "+fifo)

	waitFor(t, "carol invite issuer command finished", 60*time.Second, func() (bool, string) {
		out, _ := execInOK("server", "cat "+issuerLog+" 2>/dev/null")
		return inviteExitRe.MatchString(out), out
	})
	iOut, _ := execInOK("server", "cat "+issuerLog+" 2>/dev/null")
	if m := inviteExitRe.FindStringSubmatch(iOut); m == nil || m[1] != "0" {
		fatalf(t, "carol invite issuer command did not exit 0:\n%s", iOut)
	}

	joinOut := execIn(t, "client", "cat "+joinLog)
	if strings.Contains(joinOut, "mode is unsigned") {
		fatalf(t, "configdir join printed the unsigned-mode warning:\n%s", joinOut)
	}
	if !regexp.MustCompile(`Context "carol" \(client\) created\.`).MatchString(joinOut) {
		fatalf(t, "configdir join log missing the expected context-created line:\n%s", joinOut)
	}
	execIn(t, "server", "tw server user apply carol")

	// --- The user's exact reported bug: RELATIVE --config-dir on `client test` ---
	testOut := execIn(t, "client", "cd /tmp/carolwork && tw --config-dir ./twcarol client test")
	for _, want := range []string{"[1/3] DNS —", "[2/3] HTTPS (Caddy) —", "[3/3] Xray + SSH (server auth) —"} {
		if !strings.Contains(testOut, want) {
			fatalf(t, "tw --config-dir ./twcarol client test: step %q missing or failed:\n%s", want, testOut)
		}
	}
	if strings.Contains(testOut, "unable to authenticate") || strings.Contains(testOut, "✗") {
		fatalf(t, "tw --config-dir ./twcarol client test reported a failure:\n%s", testOut)
	}
	if strings.Contains(testOut, "/usr/local/bin") {
		fatalf(t, "tw --config-dir ./twcarol client test output mentions /usr/local/bin — the old wrong-base symptom is back:\n%s", testOut)
	}

	// --- Cleanup ---
	killMatching(t, "client", "twcarol")
	execIn(t, "client", "rm -rf /tmp/carolwork /tmp/twbadcfg")
	execIn(t, "server", "printf 'y\\n' | tw server user delete carol")

	afterCtx := execIn(t, "client", "tw config current-context")
	if strings.TrimSpace(afterCtx) != strings.TrimSpace(beforeCtx) {
		fatalf(t, "client's main current-context changed across ConfigDirSafety: before %q, after %q", beforeCtx, afterCtx)
	}
}
