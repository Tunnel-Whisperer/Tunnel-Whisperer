# Self-explanatory default context names — design

Date: 2026-07-21
Status: approved

## Problem

`tw config import` without `--name` names every context after the relay
domain, and the legacy migration hardcodes `default`. On an admin machine
holding many contexts for ONE relay, every auto-name is either identical
(collision prompt) or meaningless — exactly when distinguishable names
matter most.

## Decision (user-confirmed)

Role-aware user naming, applied only when no explicit name is given:

- client bundle with a user → the ssh user, sanitized (`server-1-user`)
- admin bundle → `admin-<first DNS label of relay>` (`admin-hds-t2`),
  or `admin` when the relay is unknown
- anything else → the sanitized relay domain (today's behavior)

Explicit `--name` always wins. Collision handling is unchanged
(`ErrContextExists` → prompt / `--force`).

## Scope

- `config.DefaultContextName(role, relay, user)` — pure derivation, lives in
  `internal/config` so both `ops.ImportContext` and the legacy migration in
  `config.EnsureContextIndex` can use it. Migration falls back to `default`
  when nothing is derivable; import falls back to `tw` (prior behavior for an
  empty relay).
- `sanitizeHostname` moves to `internal/config` as `SanitizeName`;
  `ops.sanitizeHostname` becomes a thin alias (same pattern as
  `first8`/`config.ShortID`).
- Existing stored contexts are NOT renamed; `rename-context` covers that.

## Testing

- Unit: `DefaultContextName` — client-with-user, admin-with-relay,
  admin-no-relay, fallback-to-relay, empty-everything; sanitization of
  uppercase/specials.
- e2e: UserLifecycle imports alice's bundle without `--name` — assert the
  active context is named `alice`. Full suite stays green.

## Out of scope

- Renaming existing contexts automatically.
- Server-role bundle naming beyond the relay fallback.
