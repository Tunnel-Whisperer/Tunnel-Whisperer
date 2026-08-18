//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// testCertlessProbe proves the relay's verify_if_given client_auth gate opens
// nothing to a bare TLS client: neither an enrolled tenant's own tunnel path
// nor an idle enroll route is reachable without a live invite behind it.
// Runs right after the relay is provisioned + shimmed (before ServerJoin),
// so the only enrolled tenant at this point is the admin's own "relay" route
// — rendered by renderRelayConfigs as part of `tw relay create` itself.
func testCertlessProbe(t *testing.T) {
	scenario(t, "a TLS client with no client cert reaches only 404s — verify_if_given opens nothing",
		"the admin's own tunnel path (/tw/<id>/probe) with no client cert -> HTTP 404 (the CN expression can't match an empty subject)",
		"POST /enroll/<admin-tok>/start with no live invite listener behind the route -> HTTP 404 (upstream refused, Caddy's handle_errors)")

	cfgJSON := execIn(t, "admin", "tw config view --as-json")
	var cfg struct {
		Xray struct {
			UUID string `json:"uuid"`
			Path string `json:"path"`
		} `json:"xray"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		fatalf(t, "parsing admin `tw config view --as-json`: %v\n%s", err, cfgJSON)
	}
	if cfg.Xray.UUID == "" || cfg.Xray.Path == "" {
		fatalf(t, "admin config JSON missing xray.uuid/xray.path:\n%s", cfgJSON)
	}
	// tok = first 8 hex chars of the UUID with dashes stripped — the same
	// derivation internal/ops/identity.go's first8/config.ShortID use for the
	// /enroll/<tok> route and the relay's own EnrollTok in the Caddyfile.
	tok := strings.ReplaceAll(cfg.Xray.UUID, "-", "")
	if len(tok) > 8 {
		tok = tok[:8]
	}

	// Tunnel path, no client cert: falls through every @{{.ID}} CN matcher
	// (empty subject) to the catch-all. -k bypasses server-cert trust only —
	// deliberately not relying on the suite's local_certs shim being trusted
	// here, since this probe must hold regardless of that shim's state.
	probeURL := "https://" + domain + cfg.Xray.Path + "/probe"
	out := execIn(t, "client", "curl -sk -o /dev/null -w '%{http_code}' "+probeURL)
	if strings.TrimSpace(out) != "404" {
		fatalf(t, "certless tunnel-path probe %s: got %q, want 404", probeURL, out)
	}

	// Enroll route, no live invite: the admin's own /enroll/<tok>/* route
	// exists in the Caddyfile (EnrollPort is always derived/rendered), but
	// nothing is listening on that loopback port unless an InviteServer/
	// InviteUser call is actually in flight — so the reverse_proxy upstream
	// refuses the connection and handle_errors turns that into a 404.
	enrollURL := "https://" + domain + "/enroll/" + tok + "/start"
	out = execIn(t, "client", "curl -sk -o /dev/null -w '%{http_code}' -X POST -d '{}' "+enrollURL)
	if strings.TrimSpace(out) != "404" {
		fatalf(t, "idle enroll-route probe %s: got %q, want 404", enrollURL, out)
	}
}

// mintInvite starts `tw relay invite` (or `--ttl <d>` when ttlFlag is
// non-empty) detached on the admin, waits for it to print its code, and
// returns the code plus the log path it's writing to (so callers can poll
// for TW_INVITE_EXIT later). Unlike runInviteExchange's issuer helper, this
// never needs the FIFO stdin trick: every path this scenario drives is
// denied before the issuer ever reaches the interactive SAS-confirm prompt
// (ConfirmSAS is only called after an offer decrypts successfully), so a
// plain detached run with no stdin plumbing is enough.
func mintInvite(t *testing.T, ttlFlag, logPath string) string {
	t.Helper()
	execIn(t, "admin", "rm -f "+logPath)
	execDetached(t, "admin",
		"(tw relay invite"+ttlFlag+" > "+logPath+" 2>&1; echo \"TW_INVITE_EXIT $?\" >> "+logPath+")")
	var code string
	waitFor(t, "invite minted ("+logPath+")", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "cat "+logPath+" 2>/dev/null")
		if m := inviteCodeRe.FindStringSubmatch(out); m != nil {
			code = m[1]
			return true, ""
		}
		return false, out
	})
	return code
}

// withWrongLastWord swaps the final word of an invite code ("zebra" — not in
// the embedded 256-word BIP-39 slice, so it can never collide with a real
// code) while keeping the tok and the SPAKE2-irrelevant NN segment intact:
// this is a code that routes to the right invite but authenticates as the
// wrong PAKE password.
func withWrongLastWord(t *testing.T, code string) string {
	t.Helper()
	parts := strings.Split(code, "-")
	if len(parts) != 4 {
		fatalf(t, "invite code %q does not have the expected tok-NN-word-word shape", code)
	}
	parts[3] = "zebra"
	return strings.Join(parts, "-")
}

// serverIDSet extracts the SERVER-ID column (first field of every data row)
// from `tw relay get-servers` output, ignoring the header and the "No
// servers enrolled." empty case. Used as a set (not a raw-string compare)
// because the TUNNEL up/down column for pre-existing tenants can legitimately
// flap between two captures without any tenant having been added or removed.
func serverIDSet(out string) map[string]bool {
	ids := map[string]bool{}
	for _, line := range splitLines(out) {
		if line == "" || strings.HasPrefix(line, "SERVER-ID") || strings.Contains(line, "No servers enrolled") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			ids[fields[0]] = true
		}
	}
	return ids
}

// testInviteBurn drives the abuse cases around one-time invites: a
// wrong-code redemption still burns the invite (Redeem() happens before the
// PAKE password is ever checked), a same-invite replay with the correct code
// then fails too, and an invite left past its TTL can never enroll anyone —
// throughout, no server is ever actually admitted to the relay. Registered
// after the ServerJoin-family scenarios (ServerJoin, SecondTenant) so it
// doesn't perturb their timing, and reuses server2 — dark and unenrolled
// since SecondTenant's own un-enroll — as the enrollee for every failed
// attempt here.
func testInviteBurn(t *testing.T) {
	scenario(t, "invite abuse cases never enroll a server: a wrong-code redemption burns the invite, a correct-code replay then also fails, and an expired invite is equally unusable",
		"tw relay invite mints a one-time code",
		"tw join with the right tok/NN but a wrong final word fails ('code did not match') — the invite is burned by this first /start regardless of the wrong password",
		"tw join again with the SAME correct code then also fails ('already used') — Redeem() rejects any second /start on a burned invite before any PAKE work happens",
		"a fresh invite left past its --ttl 5s can't enroll anyone: the join fails and the issuer's own AwaitOffer gives up on the same deadline",
		"throughout, tw relay get-servers gains no new tenant — no abuse path ever enrolls a server")

	before := serverIDSet(execIn(t, "admin", "tw relay get-servers"))

	// Defensive: a prior interrupted run of this scenario could have left a
	// still-hanging issuer.
	killMatching(t, "admin", "tw relay invite")

	// --- wrong code burns the invite; a same-invite replay then also fails ---
	code := mintInvite(t, "", "/shared/invite-burn-1.log")

	wrongCode := withWrongLastWord(t, code)
	wrongOut, wrongErr := execInOK("server2", "tw join "+domain+" "+wrongCode)
	if wrongErr == nil {
		fatalf(t, "join with a wrong-last-word code unexpectedly succeeded:\n%s", wrongOut)
	}
	if !strings.Contains(wrongOut, "code did not match") {
		fatalf(t, "wrong-code join: expected a 'code did not match' failure, got:\n%s", wrongOut)
	}

	correctOut, correctErr := execInOK("server2", "tw join "+domain+" "+code)
	if correctErr == nil {
		fatalf(t, "join with the correct code, after the invite was already burned by the wrong-code attempt, unexpectedly succeeded:\n%s", correctOut)
	}
	if !strings.Contains(correctOut, "already used") {
		fatalf(t, "correct-code replay of a burned invite: expected 'already used', got:\n%s", correctOut)
	}

	issuerOut1, _ := execInOK("admin", "cat /shared/invite-burn-1.log")
	if strings.Contains(issuerOut1, "enrolled") {
		fatalf(t, "issuer log shows a successful enrollment despite both redemptions being invalid:\n%s", issuerOut1)
	}
	t.Logf("issuer-1 log after both invalid redemptions (still blocked awaiting an offer that will never arrive):\n%s", issuerOut1)
	killMatching(t, "admin", "tw relay invite")

	// --- an invite left past its TTL is equally unusable ---
	code2 := mintInvite(t, " --ttl 5s", "/shared/invite-burn-2.log")
	time.Sleep(6 * time.Second)

	expiredOut, expiredErr := execInOK("server2", "tw join "+domain+" "+code2)
	if expiredErr == nil {
		fatalf(t, "join against an invite left past its TTL unexpectedly succeeded:\n%s", expiredOut)
	}

	// The issuer's own AwaitOffer races the same inv.Expires deadline as the
	// Redeem() expiry check (a context.WithDeadline and a time.Timer both
	// armed off the identical timestamp), so which exact message wins is not
	// controlled by this test — assert on the outcome (a self-terminated,
	// non-enrolling issuer), not a single exact string.
	waitFor(t, "expired-invite issuer exits", 30*time.Second, func() (bool, string) {
		out, _ := execInOK("admin", "cat /shared/invite-burn-2.log")
		return inviteExitRe.MatchString(out), out
	})
	issuerOut2, _ := execInOK("admin", "cat /shared/invite-burn-2.log")
	if strings.Contains(issuerOut2, "enrolled") {
		fatalf(t, "issuer log shows a successful enrollment for an invite that was left to expire:\n%s", issuerOut2)
	}
	if !strings.Contains(issuerOut2, "expired") && !strings.Contains(issuerOut2, "deadline exceeded") {
		fatalf(t, "issuer log does not show an expiry-driven exit:\n%s", issuerOut2)
	}
	t.Logf("issuer-2 (--ttl 5s) log:\n%s", issuerOut2)
	killMatching(t, "admin", "tw relay invite")

	after := serverIDSet(execIn(t, "admin", "tw relay get-servers"))
	if len(after) != len(before) {
		fatalf(t, "tw relay get-servers tenant count changed across the invite-abuse scenario: before=%v after=%v", before, after)
	}
	for id := range after {
		if !before[id] {
			fatalf(t, "tw relay get-servers gained an unexpected tenant %q: before=%v after=%v", id, before, after)
		}
	}
}
