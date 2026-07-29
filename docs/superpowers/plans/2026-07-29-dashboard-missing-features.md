# Dashboard Missing Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dashboard mirrors the CLI's relay tenant management (get-servers / enroll / un-enroll) and context switching, and the last-skipped Dashboard e2e scenario becomes real.

**Architecture:** CLI-first — zero new `ops` APIs. Three new relay-gated JSON endpoints + a `/servers` page (data loaded client-side), a Contexts card on the existing Config page using the two context endpoints that already exist, and a `pageData.Context` field surfaced in the nav. e2e drives the server daemon's already-running dashboard plus a detached `tw dashboard` on the admin container over curl.

**Tech Stack:** Go `net/http` + `html/template` (existing dashboard stack), vanilla JS (existing `api`/`$` helpers), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-29-dashboard-missing-features-design.md`

## Global Constraints

- Reuse ops methods only: `GetServerDetails`, `DecodeJoinRequest`, `EnrollServer`, `UnenrollServer`, `ListContexts`, `UseContext`, `CurrentContext`, `Mode`. No new ops APIs.
- Relay-only endpoints return 403 with a plain-text reason in any other mode.
- JSON field names on the wire are the Go structs' existing encodings: `ops.ServerDetail` → `server_id`, `remote_port`, `enrolled_at`, `Path`, `TunnelUp`; `ops.ContextInfo` → `Name`, `ID`, `Role`, `User`, `Relay`, `Current`.
- Enroll response download filename: `tw_join_response_<server-id>.json` (CLI convention).
- Verify at the end: `go build ./...`, `go vet ./...` (+ `-tags e2e`), full `make e2e` with Dashboard as a passing scenario; `e2e/coverage.yaml` maps `dashboard` to the scenario (exemption removed).

---

### Task 1: Relay servers API endpoints

**Files:**
- Modify: `internal/dashboard/handlers_api.go` (append)
- Modify: `internal/dashboard/server.go` (routes, after the `/api/config/use-context` line)

**Interfaces:**
- Consumes: `s.ops.Mode()`, `s.ops.GetServerDetails() ([]ops.ServerDetail, error)`, `ops.DecodeJoinRequest([]byte) (*ops.JoinRequest, error)`, `s.ops.EnrollServer(*ops.JoinRequest, ops.ProgressFunc) (*ops.JoinResponse, error)`, `resp.Encode() ([]byte, error)`, `s.ops.UnenrollServer(string, ops.ProgressFunc) error`.
- Produces: `GET /api/servers`, `POST /api/servers/enroll` (multipart field `request`), `POST /api/servers/unenroll` (`{"server_id":...}`); helper `(s *Server) requireDashboardMode(w, mode) bool`. Tasks 2 and 4 rely on these routes.

- [ ] **Step 1: Append handlers to `internal/dashboard/handlers_api.go`**

```go
// requireDashboardMode is the dashboard's requireMode: true if the profile
// runs in the wanted mode, else a 403 with the reason is written.
func (s *Server) requireDashboardMode(w http.ResponseWriter, mode string) bool {
	if got := s.ops.Mode(); got != mode {
		http.Error(w, fmt.Sprintf("only available in %s mode (this profile: %q)", mode, got), http.StatusForbidden)
		return false
	}
	return true
}

// apiServers mirrors `tw relay get-servers`: registry + ONE live relay
// query. Relay unreachable is an error, never a stale table.
func (s *Server) apiServers(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashboardMode(w, "relay") {
		return
	}
	details, err := s.ops.GetServerDetails()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if details == nil {
		details = []ops.ServerDetail{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(details)
}

// apiEnrollServer mirrors `tw relay enroll-server`: multipart upload of the
// join request, join-response JSON returned as a download.
func (s *Server) apiEnrollServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireDashboardMode(w, "relay") {
		return
	}
	f, _, err := r.FormFile("request")
	if err != nil {
		http.Error(w, "missing join-request upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		http.Error(w, "reading upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	req, err := ops.DecodeJoinRequest(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.ops.EnrollServer(req, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out, err := resp.Encode()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "tw_join_response_"+resp.ServerID+".json"))
	_, _ = w.Write(out)
}

// apiUnenrollServer mirrors `tw relay un-enroll-server --yes` (the
// confirmation lives in the browser).
func (s *Server) apiUnenrollServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireDashboardMode(w, "relay") {
		return
	}
	var req struct {
		ServerID string `json:"server_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ServerID == "" {
		http.Error(w, "bad request: server_id required", http.StatusBadRequest)
		return
	}
	if err := s.ops.UnenrollServer(req.ServerID, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Add `"io"` to the file's imports if absent.

- [ ] **Step 2: Register routes in `internal/dashboard/server.go`**

After `s.mux.HandleFunc("/api/config/use-context", s.apiUseContext)`:

```go
	s.mux.HandleFunc("/api/servers", s.apiServers)
	s.mux.HandleFunc("/api/servers/enroll", s.apiEnrollServer)
	s.mux.HandleFunc("/api/servers/unenroll", s.apiUnenrollServer)
```

- [ ] **Step 3: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/dashboard/handlers_api.go internal/dashboard/server.go
git commit -m "feat(dashboard): relay servers API (list/enroll/un-enroll)"
```

---

### Task 2: Servers page (relay mode)

**Files:**
- Create: `internal/dashboard/templates/pages/servers.html`
- Create: `internal/dashboard/static/js/servers.js`
- Modify: `internal/dashboard/handlers_pages.go` (append `handleServers`)
- Modify: `internal/dashboard/server.go` (route `/servers` after `/relay/wizard`)
- Modify: `internal/dashboard/templates/partials/nav.html` (Servers link in the relay block)

**Interfaces:**
- Consumes: Task 1's endpoints; `s.renderPage`, `pageData` (Task 3 adds `Context` — this task builds `pageData` the pre-existing way and Task 3 migrates it).
- Produces: `GET /servers` page; nav entry `Active: "servers"`.

- [ ] **Step 1: `handleServers` in `internal/dashboard/handlers_pages.go`**

```go
// handleServers is the relay-mode tenant page; other modes bounce home.
func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	mode := s.ops.Mode()
	if mode != "relay" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderPage(w, "servers", struct {
		pageData
	}{
		pageData: pageData{Title: "Servers", Active: "servers", Mode: mode},
	})
}
```

Route in `server.go` after the `/relay/wizard` line:

```go
	s.mux.HandleFunc("/servers", s.handleServers)
```

- [ ] **Step 2: `internal/dashboard/templates/pages/servers.html`**

```html
{{define "content"}}
<h1>Servers</h1>
<p class="text-dim mb-16">Servers enrolled on this relay. The tunnel column is queried live from the relay.</p>

<div class="card mb-16">
  <div class="card-header">
    <h2>Enrolled Servers</h2>
    <button class="btn" onclick="loadServers()">Refresh</button>
  </div>
  <div id="servers-loading" class="text-dim">Querying relay…</div>
  <div id="servers-error" class="alert alert-error hidden"></div>
  <table class="table hidden" id="servers-table">
    <thead><tr><th>Server ID</th><th>Path</th><th>Port</th><th>Enrolled</th><th>Tunnel</th><th></th></tr></thead>
    <tbody id="servers-rows"></tbody>
  </table>
  <div id="servers-empty" class="text-dim hidden">No servers enrolled.</div>
</div>

<div class="card">
  <div class="card-header">
    <h2>Enroll a Server</h2>
  </div>
  <p class="text-dim mb-16">Upload the join request (tw_join_*.json) generated by <code>tw server join-relay</code> on the joining server. When the enrollment completes, the join response downloads — send it back and apply it there with <code>tw server join-relay --apply</code>.</p>
  <div class="form-group">
    <input type="file" id="enroll-file" accept=".json">
  </div>
  <button class="btn btn-primary" id="btn-enroll" onclick="enrollServer()">Enroll</button>
  <div id="enroll-error" class="alert alert-error mt-16 hidden"></div>
  <div id="enroll-success" class="alert alert-success mt-16 hidden"></div>
</div>
{{end}}

{{define "scripts"}}
<script src="/static/js/servers.js"></script>
{{end}}
```

- [ ] **Step 3: `internal/dashboard/static/js/servers.js`**

```js
// ── Enrolled servers (relay mode) ───────────────────────────────────────────

function escText(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

function fmtEnrolled(iso) {
  if (!iso) return '—';
  return iso.replace('T', ' ').replace(/:\d\d(Z|[+-].*)?$/, '');
}

async function loadServers() {
  const loading = $('#servers-loading'), errBox = $('#servers-error');
  const table = $('#servers-table'), rows = $('#servers-rows'), empty = $('#servers-empty');
  loading.classList.remove('hidden');
  errBox.classList.add('hidden');
  table.classList.add('hidden');
  empty.classList.add('hidden');
  try {
    const list = await api.get('/api/servers');
    loading.classList.add('hidden');
    if (!list.length) { empty.classList.remove('hidden'); return; }
    rows.innerHTML = list.map(s => {
      const badge = s.TunnelUp
        ? '<span class="badge badge-green">up</span>'
        : '<span class="badge badge-dim">down</span>';
      return `<tr>
        <td>${escText(s.server_id)}</td>
        <td>${escText(s.Path)}</td>
        <td>${escText(s.remote_port)}</td>
        <td>${escText(fmtEnrolled(s.enrolled_at))}</td>
        <td>${badge}</td>
        <td><button class="btn btn-danger" onclick="unenrollServer('${escText(s.server_id)}', ${s.remote_port})">Un-enroll</button></td>
      </tr>`;
    }).join('');
    table.classList.remove('hidden');
  } catch (err) {
    loading.classList.add('hidden');
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  }
}

async function unenrollServer(serverID, port) {
  if (!confirm(`Un-enroll ${serverID} (port ${port})? Its relay access and all its live connections end immediately.`)) return;
  const errBox = $('#servers-error');
  errBox.classList.add('hidden');
  try {
    const resp = await fetch('/api/servers/unenroll', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ server_id: serverID }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    loadServers();
  } catch (err) {
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  }
}

async function enrollServer() {
  const fileInput = $('#enroll-file'), btn = $('#btn-enroll');
  const errBox = $('#enroll-error'), okBox = $('#enroll-success');
  errBox.classList.add('hidden');
  okBox.classList.add('hidden');
  if (!fileInput.files.length) {
    errBox.textContent = 'Choose a tw_join_*.json file first.';
    errBox.classList.remove('hidden');
    return;
  }
  btn.disabled = true;
  btn.textContent = 'Enrolling…';
  try {
    const form = new FormData();
    form.append('request', fileInput.files[0]);
    const resp = await fetch('/api/servers/enroll', { method: 'POST', body: form });
    if (!resp.ok) throw new Error(await resp.text());
    const blob = await resp.blob();
    const dispo = resp.headers.get('Content-Disposition') || '';
    const m = dispo.match(/filename="?([^";]+)"?/);
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = m ? m[1] : 'tw_join_response.json';
    a.click();
    URL.revokeObjectURL(a.href);
    okBox.textContent = 'Enrolled. The join response downloaded — send it to the server and apply it there.';
    okBox.classList.remove('hidden');
    fileInput.value = '';
    loadServers();
  } catch (err) {
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  } finally {
    btn.disabled = false;
    btn.textContent = 'Enroll';
  }
}

document.addEventListener('DOMContentLoaded', loadServers);
```

- [ ] **Step 4: Nav link in `internal/dashboard/templates/partials/nav.html`**

In the `{{if eq .Mode "relay"}}` block, after the Relay link:

```html
    <li><a href="/servers" class="{{if eq .Active "servers"}}active{{end}}">Servers</a></li>
```

- [ ] **Step 5: Build, vet, template smoke**

Run: `go build ./... && go vet ./...` then
`TW_CONFIG_DIR=$(mktemp -d) go run ./cmd/tw dashboard --port 18099 &` — GET `http://localhost:18099/servers` must redirect (empty mode ⇒ not relay), then kill it. (If `go run` is awkward, `make build` + `./bin/tw`.)
Expected: clean build; `/servers` responds 303 to `/`.

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard/templates/pages/servers.html internal/dashboard/static/js/servers.js internal/dashboard/handlers_pages.go internal/dashboard/server.go internal/dashboard/templates/partials/nav.html
git commit -m "feat(dashboard): relay Servers page (live table, enroll upload, un-enroll)"
```

---

### Task 3: Context switcher card + nav context

**Files:**
- Modify: `internal/dashboard/server.go` (`pageData` gains `Context string`; add `newPageData`)
- Modify: `internal/dashboard/handlers_pages.go` (all `pageData{Title: ..., Active: ..., Mode: mode}` literals → `s.newPageData(...)`; `handleServers` from Task 2 included)
- Modify: `internal/dashboard/templates/partials/nav.html` (context badge)
- Modify: `internal/dashboard/templates/pages/config.html` (Contexts card)
- Modify: `internal/dashboard/static/js/config.js` (load/switch functions)

**Interfaces:**
- Consumes: existing `GET /api/config/contexts` (JSON `[]ops.ContextInfo`: `Name`/`ID`/`Role`/`User`/`Relay`/`Current`), existing `POST /api/config/use-context` (`{"name":...}`), `s.ops.CurrentContext() (string, error)`.
- Produces: `pageData.Context`; `(s *Server) newPageData(title, active string) pageData` — used by every page handler.

- [ ] **Step 1: `pageData.Context` + `newPageData` in `internal/dashboard/server.go`**

```go
type pageData struct {
	Title   string
	Active  string // nav highlight
	Mode    string // "server", "client", "relay", or ""
	Context string // active context name ("" if none stored)
}

// newPageData fills the fields every page shares. CurrentContext is a local
// index read; an error just leaves the badge empty.
func (s *Server) newPageData(title, active string) pageData {
	ctx, _ := s.ops.CurrentContext()
	return pageData{Title: title, Active: active, Mode: s.ops.Mode(), Context: ctx}
}
```

- [ ] **Step 2: Migrate every page handler literal**

In `internal/dashboard/handlers_pages.go`, replace each
`pageData: pageData{Title: "X", Active: "y", Mode: mode},` with
`pageData: s.newPageData("X", "y"),` (keep any handler-local `mode`
variable where it's used elsewhere; drop it if now unused).

- [ ] **Step 3: Nav badge in `nav.html`**

Replace the `.navbar-mode` div content:

```html
  <div class="navbar-mode">
    {{if .Context}}<span class="badge badge-dim">{{.Context}}</span>{{end}}
    <span class="badge badge-dim">{{.Mode}}</span>
  </div>
```

- [ ] **Step 4: Contexts card in `config.html`** (before the Version card)

```html
<div class="card mb-16">
  <div class="card-header">
    <h2>Contexts</h2>
  </div>
  <p class="text-dim mb-16">Stored relay contexts. Switching re-seals the current profile, activates the selected one, and reloads the page.</p>
  <table class="table" id="contexts-table">
    <thead><tr><th></th><th>Name</th><th>ID</th><th>Role</th><th>User</th><th>Relay</th><th></th></tr></thead>
    <tbody id="contexts-rows"><tr><td colspan="7" class="text-dim">Loading…</td></tr></tbody>
  </table>
  <div id="contexts-error" class="alert alert-error mt-16 hidden"></div>
</div>
```

- [ ] **Step 5: JS in `config.js`**

```js
// ── Contexts ────────────────────────────────────────────────────────────────

async function loadContexts() {
  const rows = $('#contexts-rows');
  if (!rows) return;
  try {
    const list = (await api.get('/api/config/contexts')) || [];
    if (!list.length) {
      rows.innerHTML = '<tr><td colspan="7" class="text-dim">No stored contexts.</td></tr>';
      return;
    }
    rows.innerHTML = '';
    for (const c of list) {
      const tr = document.createElement('tr');
      const cells = [c.Current ? '●' : '', c.Name, c.ID || '—', c.Role || '—', c.User || '—', c.Relay || '—'];
      for (const v of cells) {
        const td = document.createElement('td');
        td.textContent = v;
        tr.appendChild(td);
      }
      const td = document.createElement('td');
      if (!c.Current) {
        const btn = document.createElement('button');
        btn.className = 'btn';
        btn.textContent = 'Switch';
        btn.onclick = () => switchContext(c.Name);
        td.appendChild(btn);
      }
      tr.appendChild(td);
      rows.appendChild(tr);
    }
  } catch (err) {
    const errBox = $('#contexts-error');
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  }
}

async function switchContext(name) {
  if (!confirm(`Switch to context "${name}"? The current profile is re-sealed and the page reloads.`)) return;
  const errBox = $('#contexts-error');
  errBox.classList.add('hidden');
  try {
    const resp = await fetch('/api/config/use-context', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    location.reload();
  } catch (err) {
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  }
}

document.addEventListener('DOMContentLoaded', loadContexts);
```

- [ ] **Step 6: Build + vet + template render check**

Run: `go build ./... && go vet ./... && go test ./internal/dashboard/ 2>/dev/null; true`
Then smoke as in Task 2 step 5: `/config` renders 200 and contains "Contexts".

- [ ] **Step 7: Commit**

```bash
git add internal/dashboard/server.go internal/dashboard/handlers_pages.go internal/dashboard/templates/partials/nav.html internal/dashboard/templates/pages/config.html internal/dashboard/static/js/config.js
git commit -m "feat(dashboard): context switcher card and nav context badge"
```

---

### Task 4: Dashboard e2e scenario

**Files:**
- Create: `e2e/dashboard_test.go`
- Modify: `e2e/e2e_test.go` (delete the `testDashboard` skip stub; keep the `{"Dashboard", testDashboard}` registration)
- Modify: `e2e/coverage.yaml` (`"dashboard"` maps to the scenario instead of the exemption)

**Interfaces:**
- Consumes: harness `scenario`, `execIn`, `execInOK`, `execDetached`, `killMatching`, `waitFor`, `fatalf`; the server container's daemon dashboard already on `127.0.0.1:8080` (UserLifecycle curls it today).
- Produces: passing `TestE2E/Dashboard`.

- [ ] **Step 1: `e2e/dashboard_test.go`**

```go
//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// testDashboard proves the dashboard serves per-role and mirrors the CLI:
// the server daemon's dashboard reports status, and the admin's dashboard
// exposes tenant management (live server table) and the context store.
// Read-only apart from a bogus un-enroll that must be rejected.
func testDashboard(t *testing.T) {
	scenario(t, "the dashboard serves per-role and mirrors CLI features",
		"the server daemon's dashboard on :8080 serves the status page and reports mode server",
		"tw dashboard on the admin serves the relay home and the Servers page",
		"admin /api/servers lists server-1 with its live tunnel up (mirrors tw relay get-servers)",
		"admin /api/config/contexts returns the current context",
		"a bogus un-enroll POST is rejected and get-servers still lists server-1")

	// Server: the daemon started in ServerJoin already serves the dashboard.
	out := execIn(t, "server", "curl -sf http://127.0.0.1:8080/")
	if !strings.Contains(out, "Tunnel Whisperer") {
		fatalf(t, "server dashboard status page did not render:\n%.400s", out)
	}
	out = execIn(t, "server", "curl -sf http://127.0.0.1:8080/api/status")
	if !strings.Contains(out, `"server"`) {
		fatalf(t, "server /api/status does not report mode server:\n%s", out)
	}

	// Admin: no daemon runs there — start the standalone dashboard.
	killMatching(t, "admin", "tw dashboard")
	execDetached(t, "admin", "tw dashboard > /shared/dash-admin.log 2>&1")
	defer killMatching(t, "admin", "tw dashboard")
	waitFor(t, "admin dashboard up", 30*time.Second, func() (bool, string) {
		out, err := execInOK("admin", "curl -sf http://127.0.0.1:8080/api/status")
		return err == nil, out
	})

	out = execIn(t, "admin", "curl -sf http://127.0.0.1:8080/")
	if !strings.Contains(out, "Relay") {
		fatalf(t, "admin dashboard home is not the relay view:\n%.400s", out)
	}
	out = execIn(t, "admin", "curl -sf http://127.0.0.1:8080/servers")
	if !strings.Contains(out, "Enroll a Server") {
		fatalf(t, "admin /servers page did not render the tenant view:\n%.400s", out)
	}

	// Live tenant table mirrors `tw relay get-servers`: server-1 up.
	serverHost := strings.TrimSpace(execIn(t, "server", "hostname"))
	out = execIn(t, "admin", "curl -sf http://127.0.0.1:8080/api/servers")
	if !strings.Contains(out, serverHost) || !strings.Contains(out, `"TunnelUp":true`) {
		fatalf(t, "/api/servers does not list %s-* with TunnelUp true:\n%s", serverHost, out)
	}

	// Context store is exposed.
	out = execIn(t, "admin", "curl -sf http://127.0.0.1:8080/api/config/contexts")
	if !strings.Contains(out, `"Current":true`) {
		fatalf(t, "/api/config/contexts has no current context:\n%s", out)
	}

	// A bogus un-enroll must be rejected and change nothing.
	code := strings.TrimSpace(execIn(t, "admin",
		`curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{"server_id":"nope-1"}' http://127.0.0.1:8080/api/servers/unenroll`))
	if code == "200" || code == "204" {
		fatalf(t, "bogus un-enroll was accepted (HTTP %s)", code)
	}
	out = execIn(t, "admin", "tw relay get-servers")
	if !strings.Contains(out, serverHost) {
		fatalf(t, "server-1 disappeared after the rejected un-enroll:\n%s", out)
	}
}
```

- [ ] **Step 2: Remove the skip stub**

In `e2e/e2e_test.go` delete the old `testDashboard` func (the
`skipScenario(...)` stub and its comment). The scenario registration line
stays.

- [ ] **Step 3: coverage.yaml**

Replace:

```yaml
  "dashboard":               {exempt: "standalone dashboard command; the in-server dashboard is covered by TestE2E/Dashboard"}
```

with:

```yaml
  "dashboard":               {scenario: "Dashboard"}
```

(Match the file's existing mapping syntax for non-exempt entries — copy a
neighboring `scenario:` entry's exact shape.)

- [ ] **Step 4: Compile + coverage gate**

Run: `go vet -tags e2e ./e2e/ && go test ./internal/cli/ -run TestCoverage`
Expected: clean (adjust the run pattern to the actual coverage test name in `internal/cli/coverage_test.go`).

- [ ] **Step 5: Full e2e**

Run: `make e2e`
Expected: 12 passed, 0 skipped, Dashboard PASS.

- [ ] **Step 6: Commit**

```bash
git add e2e/dashboard_test.go e2e/e2e_test.go e2e/coverage.yaml
git commit -m "test(e2e): real Dashboard scenario (status, tenant mgmt, contexts)"
```
