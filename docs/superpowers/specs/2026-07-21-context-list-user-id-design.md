# Context list: USER + ID columns, ID as selector — design

Date: 2026-07-21
Status: approved

## Problem

`tw config get-contexts` shows only NAME/ROLE/RELAY. With several contexts
pointing at the same relay (e.g. `hds-t2-mint-tunnel-com` and `default`),
rows are hard to tell apart, and there is no stable identifier to switch by
when names look alike.

## Decisions (user-confirmed)

- **USER column: client user only.** Show `client.ssh_user` for client
  contexts; blank for server and admin rows (they have no named user).
- **ID is a selector, not just display.** `use-context`, `delete-context`,
  `rename-context <old>` and `export` accept either the context name or the
  short ID.

## ID definition

First 8 hex characters of the context's `xray.uuid` — the same short-id
convention `deriveServerID` already uses. Stable across export/import and
machines because it derives from the profile itself. A fresh unconfigured
context (from `new-context`) has no UUID yet and shows a blank ID until it
is set up.

## Approach (chosen: extend the index, backfill lazily)

`config.ContextMeta` gains `user` and `id` fields, captured everywhere
role/relay already are: import, switch-away reseal, `new-context` reseal,
legacy migration, and the live-config override for the current row. On
`ListContexts`, a stored context with an empty ID whose bundle exists is
backfilled once by decrypting its (passphrase-less) bundle, and the index is
re-saved. Rejected alternative: deriving from bundles on every list — never
stale, but repeated decrypt work and it diverges from how role/relay are
already cached.

## Output

```
CURRENT   NAME                     ID         ROLE     USER    RELAY
*         hds-t2-mint-tunnel-com   3f9ac21b   client   alice   hds-t2.mint-tunnel.com
          default                  b0e51c77   admin            hds-t2.mint-tunnel.com
```

The dashboard's `/api/config/contexts` JSON picks the new `ContextInfo`
fields up automatically (additive).

## Selector resolution

Shared resolver used by `use-context`, `delete-context`,
`rename-context <old>`, `export`:

1. Exact name match wins.
2. Otherwise a case-insensitive match on the full 8-char ID; unique → that
   context.
3. Two contexts sharing an ID (same bundle imported twice under different
   names) → error "ambiguous — use the name".
4. No match → "no such context".

## Testing

- Unit: pure resolver function (name wins, ID match, ambiguous, miss);
  bundle meta extraction returns user + uuid.
- e2e: implement the `Contexts` scenario placeholder — real context
  lifecycle on the admin container (get-contexts columns, `new-context`,
  switch back via ID selector, rename, delete, `current-context`), plus a
  client-container check that USER shows the ssh user. `coverage.yaml`
  already maps the `config *-context` commands to `TestE2E/Contexts`.

## Out of scope

- Full Task 8 context e2e (export/import round-trip, running-client
  reconnect on switch) — the scenario documents exactly what it verifies.
- Partial-ID prefix matching (full 8 chars only).
