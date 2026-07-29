# Dashboard: relay tenant management, context switcher, e2e scenario

**Date:** 2026-07-29
**Status:** Approved

## Principle

CLI-first (CLAUDE.md): every capability here already exists in the CLI via
`internal/ops` — the dashboard only mirrors it, reusing the exact same ops
methods (`GetServerDetails`, `DecodeJoinRequest`, `EnrollServer`,
`UnenrollServer`, `ListContexts`, `UseContext`). No new ops APIs.

## 1. Relay tenant management — "Servers" page (relay mode only)

- Nav gains **Servers** when `Mode == "relay"`; route `/servers`.
- Page = `tw relay get-servers` as a table: SERVER-ID, PATH, PORT, ENROLLED,
  TUNNEL (green "up" / dim "down" badge), from `ops.GetServerDetails()`.
  Relay unreachable ⇒ error banner, NO stale table (mirrors the CLI's
  fail-hard decision). Empty registry ⇒ "no servers enrolled" empty state.
- **Enroll card**: file input for a `tw_join_*.json` →
  `POST /api/servers/enroll` (multipart field `request`) → server runs
  `DecodeJoinRequest` + `EnrollServer(req, nil)` → responds with the
  join-response JSON as a download:
  `Content-Disposition: attachment; filename="tw_join_response_<id>.json"`
  (same convention as the CLI). Synchronous; the page shows a spinner.
- **Un-enroll**: per-row button → JS `confirm()` naming id + port →
  `POST /api/servers/unenroll` body `{"server_id": "..."}` → reload table.
- New endpoints, all mode-gated to `relay` (else 403 — the dashboard
  equivalent of `requireMode`):
  - `GET  /api/servers` → `[]ops.ServerDetail` JSON (`null` ⇒ `[]`)
  - `POST /api/servers/enroll` → join-response JSON download
  - `POST /api/servers/unenroll` → 200 or error text

## 2. Context switcher

- Config page gains a **Contexts card**: table CURRENT (● marker) / NAME /
  ID / ROLE / USER / RELAY from existing `GET /api/config/contexts`;
  a **Switch** button on every non-current row posts
  `{"name": "<name>"}` to existing `POST /api/config/use-context`, then
  `location.reload()` — the mode (and whole nav) may change with the
  context.
- Nav shows the current context name beside the mode badge: `pageData`
  gains `Context string`, filled by every page handler from a small helper
  (`ops.CurrentContext()` if present, else the contexts list's Current
  entry).
- Scope: read + switch only. new/rename/delete stay CLI-only.

## 3. Dashboard e2e scenario (replaces the last skip)

- Position: after SecondTenant (server-1 enrolled + tunnel up), before
  RelayResilience. Non-disruptive: read-only apart from a rejected bogus
  un-enroll.
- Flow: start `tw dashboard` detached on **server** and **admin**
  containers; wait for HTTP 200 on `localhost:8080`; then via curl:
  - server: `/` renders (contains "Tunnel Whisperer"), `/api/status` JSON
    reports mode `server`.
  - admin: `/` renders the relay home; `/servers` lists server-1's id with
    an "up" tunnel badge; `/api/servers` JSON contains the server-1 row;
    `/api/config/contexts` includes the current context; bogus
    `POST /api/servers/unenroll {"server_id":"nope-1"}` returns an error
    status and `tw relay get-servers` still lists server-1 afterward.
  - kill both dashboards (`killMatching`) at the end.
- `e2e/coverage.yaml`: map `dashboard` to the Dashboard scenario (replace
  the exemption).

## Testing

- Unit: any new pure helpers (e.g. current-context lookup, enroll filename)
  — thin handlers over ops follow the existing no-unit-test dashboard
  pattern and are covered by the e2e scenario.
- `make e2e` must pass with Dashboard as a real (12th) passing scenario.

## Out of scope

- Dashboard actions for context new/rename/delete/export/import.
- Enroll progress streaming over SSE (sync + spinner is enough).
- Client-mode dashboard changes; auth changes; SSE/WS work.
