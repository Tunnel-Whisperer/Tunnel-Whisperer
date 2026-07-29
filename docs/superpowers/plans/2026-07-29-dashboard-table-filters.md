# Dashboard Table Filters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Live search + dropdown filters on the enrolled-servers table and the contexts table.

**Architecture:** Both tables already fetch their full lists in JS; keep the list in a module variable and re-render rows through a predicate on input/change. Toolbar markup in the templates, logic in the existing JS files. No Go changes.

**Tech Stack:** Existing vanilla JS + templates. No new deps.

**Spec:** `docs/superpowers/specs/2026-07-29-dashboard-table-filters-design.md`

## Global Constraints

- No new endpoints, no ops calls, no Go code changes (templates/JS only, plus e2e assertions).
- Case-insensitive substring search; dropdowns: servers → all/up/down, contexts → all/relay/server/client.
- "n of m" count shown only while a filter is active; empty result = one "no matches" row.
- Verify: `go build ./...` (embed picks up assets), smoke-curl the two pages for the toolbar markup, full `make e2e`.

---

### Task 1: Servers table toolbar + filter logic

**Files:**
- Modify: `internal/dashboard/templates/pages/servers.html` (toolbar between card-header and loading div)
- Modify: `internal/dashboard/static/js/servers.js` (`allServers` + `renderServers()`)

**Steps:**
- [ ] Toolbar markup after the Enrolled Servers card-header:

```html
  <div class="flex gap-8 mb-16">
    <input type="text" id="servers-search" placeholder="Search servers…" autocomplete="off" oninput="renderServers()">
    <select id="servers-tunnel-filter" onchange="renderServers()">
      <option value="">all tunnels</option>
      <option value="up">up</option>
      <option value="down">down</option>
    </select>
    <span id="servers-count" class="text-dim hidden"></span>
  </div>
```

- [ ] In `servers.js`: `let allServers = [];`; `loadServers()` stores the fetched list and calls `renderServers()`; `renderServers()` applies search (server_id, Path, remote_port, formatted enrolled) + tunnel dropdown, renders rows or a `<tr><td colspan="6">no matches</td></tr>` row, and sets `#servers-count` to `"n of m"` (hidden when no filter active). Un-enroll success re-calls `loadServers()` as today.
- [ ] `go build ./...`; smoke: `/servers` HTML contains `servers-search`.
- [ ] Commit: `feat(dashboard): search + tunnel filter on enrolled-servers table`

### Task 2: Contexts table toolbar + filter logic

**Files:**
- Modify: `internal/dashboard/templates/pages/config.html` (toolbar under the Contexts card intro)
- Modify: `internal/dashboard/static/js/config.js` (`allContexts` + `renderContexts()`)

**Steps:**
- [ ] Toolbar markup before the contexts table:

```html
  <div class="flex gap-8 mb-16">
    <input type="text" id="contexts-search" placeholder="Search contexts…" autocomplete="off" oninput="renderContexts()">
    <select id="contexts-role-filter" onchange="renderContexts()">
      <option value="">all roles</option>
      <option value="relay">relay</option>
      <option value="server">server</option>
      <option value="client">client</option>
    </select>
    <span id="contexts-count" class="text-dim hidden"></span>
  </div>
```

- [ ] In `config.js`: `let allContexts = [];`; `loadContexts()` stores and calls `renderContexts()`; predicate: search across Name/ID/Role/User/Relay + role dropdown equality; same count/no-matches behavior (colspan 7).
- [ ] `go build ./...`; smoke: `/config` HTML contains `contexts-search`.
- [ ] Commit: `feat(dashboard): search + role filter on contexts table`

### Task 3: e2e markup assertions + full verification

**Files:**
- Modify: `e2e/dashboard_test.go`

**Steps:**
- [ ] After the `/servers` page assertion add `servers-search` to the required content; after the contexts-API check add a `/config` fetch asserting `contexts-search`.
- [ ] `go vet -tags e2e ./e2e/`; full `make e2e` → 12 pass.
- [ ] Commit: `test(e2e): assert dashboard table filter toolbars render`
