# Dashboard table search + filter (servers, contexts)

**Date:** 2026-07-29
**Status:** Approved

## Scope

Pure client-side presentation on the two JS-rendered dashboard tables — no
new endpoints, no ops calls (CLI-first unaffected).

- **Servers page** (`/servers`): toolbar above the enrolled-servers table
  with a live search box (case-insensitive substring across server-id,
  path, port, enrolled) and a Tunnel dropdown (all / up / down).
- **Config → Contexts card**: search box (name, ID, role, user, relay) and
  a Role dropdown (all / relay / server / client).

## Mechanism

- Each JS file keeps the fetched list in a module variable (`allServers`,
  `allContexts`) and re-renders rows through a filter predicate on every
  `input`/`change` event (re-render, not DOM hiding).
- Empty result renders a single "no matches" row.
- A count "n of m" appears next to the card header whenever a filter is
  active (hidden when showing everything).

## Testing

- e2e Dashboard scenario asserts the search inputs are present in the
  served HTML (`servers-search` on `/servers`, `contexts-search` on
  `/config`). Filtering behavior is browser-side JS; no browser exists in
  the e2e topology, so markup presence is the assertable boundary.
- No new commands ⇒ coverage.yaml untouched.

## Out of scope

- Server-side filtering/pagination; sorting; persistence of filter state.
