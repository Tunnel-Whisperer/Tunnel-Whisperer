# Unified `tw status` Command — Design

Date: 2026-07-24
Status: approved (brainstormed with user)

## Problem

A user returning to a machine after a while has to answer "what is going on here?"
in several steps: figure out the mode (`tw config current-context` /
`get-contexts`), then run the right role status (`tw relay|server|client status`),
then maybe `tw config view` for details. There is no single entry point, and the
role-gated status commands refuse to run in the wrong mode, so guessing costs a
round-trip.

## Goal

One top-level, ungated command:

```
tw status
```

that detects the active mode/context and prints **one unified view**: the
current context's identity (the same fields `tw config get-contexts` shows, but
only the current row), the config file path, and the mode-appropriate live
status — each fact printed exactly once (no repeated Mode/Relay lines).

## Behavior

- `tw status` is a new root command, **not mode-gated**. It works in relay,
  server, and client mode alike; on a machine with no mode set it prints the
  header with `Mode: —` and a "not set up yet" hint instead of failing.
- The unified body is produced by the existing `sharedStatus()` in
  `internal/cli/status.go`, extended with a context header. Because
  `tw relay status`, `tw server status`, and `tw client status` already call
  `sharedStatus()`, all four commands print the identical unified view;
  `tw status` is a true ungated alias of whichever role status applies.
- Existing behavior preserved: daemon-vs-context mismatch warning, remote
  (gRPC daemon) vs local fallback, role gates on the three role-scoped
  variants.

## Output Format

```
$ tw status
  Context:  relay-tw-test (bef98b84)   — 3 contexts stored
  Mode:     relay
  User:     alice                      ← only for client contexts with a user
  Relay:    relay.tw.test
  Config:   /etc/tw/config/config.yaml

  Users:  2

  Relay:
    Provisioned: true
    IP:          10.0.0.5
    Provider:    manual

  Server:                              ← daemon section, exactly as today
    State:   running
    ...
```

Changes vs. the current `sharedStatus()` body, to avoid repetition:

- `Mode:` moves into the header (printed once).
- `Relay: <domain>` lives in the header; the `Relay:` section drops its
  `Domain:` line and keeps `Provisioned:` / `IP:` / `Provider:`.
- `Users:` count, daemon `Server:`/`Client:` sections, and the "(daemon not
  running …)" hint are unchanged.

Header data sources (always read locally — contexts are local state even when
the daemon answers):

- Context name + short ID + count: current row of `ops.ListContexts()`.
- User / Relay: same row (`USER`, `RELAY` columns of `get-contexts`).
- Config path: `config.FilePath()`.

## Compatibility

e2e assertions on status output only require that the output contains the relay
domain and the `Provisioned: true|false` string (`relay_install_test.go`,
`resilience_test.go`). Both survive: the domain moves to the header, the
`Provisioned:` line stays.

## Testing

- Unit: pure formatting helper for the header (given a context row + config
  path, returns the header lines) so the layout is covered without a daemon.
- e2e: in the Contexts scenario, run `tw status` on the admin and the client
  and assert the header shows the current context name and the correct mode;
  run it once on a torn-down machine (or before setup) to cover the ungated
  "not set up" path if a natural spot exists.
- Docs: add `tw status` to `docs/reference/cli.md` and mention it in the
  troubleshooting guide's "where am I?" flow.

## Round 2 — Human-Readable Status (user feedback, 2026-07-24)

After using the unified status, the user asked for three refinements:

1. **Words, not booleans.** `SSH: true` carries no meaning. Component states
   print `working` / `not working` (SSH, Xray, Tunnel in both the Server and
   Client sections); `Provisioned:` prints `yes` / `no`.
2. **Resolve the empty IP.** `RelayStatus.IP` is empty for joined servers and
   marker-less manual relays. When the stored IP is empty but a relay domain
   is known, the CLI resolves the domain via DNS (2s timeout) and prints
   `<ip> (resolved)`; if resolution fails, `—`.
3. **Show connected users.** In server mode the Users line becomes
   `Users: N (M connected)`, backed by `ops.GetOnlineUsers()` (the dashboard's
   online tracking). `StatusResponse` gains `connected_users`; the local
   (daemon-down) path correctly reports 0 connected — a stopped server has no
   sessions.

e2e impact: `resilience_test.go` asserts `Provisioned: true|false` — those
assertions (and their scenario briefs) change to `Provisioned: yes|no`.

## Out of Scope

- No JSON output flag, no `--watch`, no changes to the gRPC `GetStatus` API
  (header data is local).
- Dashboard is untouched.
