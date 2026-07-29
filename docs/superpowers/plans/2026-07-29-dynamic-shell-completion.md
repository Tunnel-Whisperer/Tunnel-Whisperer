# Dynamic Shell Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `tw config use-context <TAB>` (and every command whose positional arg names a stored object) offers the stored objects as zsh completion candidates.

**Architecture:** One new file `internal/cli/completions.go` holds pure candidate-builder funcs (unit-testable, no config dir) plus thin cobra `ValidArgsFunction` glue that reads the local stores via `ops` and attaches to the existing command vars in a single `init()`. e2e asserts through cobra's hidden `__complete` command inside the existing scenarios.

**Tech Stack:** Go 1.26, cobra (already vendored), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-29-dynamic-shell-completion-design.md`

## Global Constraints

- Completion must NEVER print an error or fall back to file completion: every helper returns `cobra.ShellCompDirectiveNoFileComp`, and any error yields `(nil, NoFileComp)`.
- Completion reads only local state (context index, users dir, server registry, config.yaml) — no relay/daemon dialing.
- Candidates are `"value\tdescription"`; omit the `\t` entirely when the description is empty.
- No mode gating inside completers (`requireMode` stays on the commands).
- Verify per CLAUDE.md: `go build ./...`, `go vet ./...`, unit tests, and full `make e2e` at the end; coverage.yaml needs no change (no new user-facing commands).

---

### Task 1: Pure candidate builders

**Files:**
- Create: `internal/cli/completions.go`
- Create: `internal/cli/completions_test.go`

**Interfaces:**
- Consumes: `ops.ContextInfo{Name,ID,Role,User,Relay}`, `ops.UserInfo{Name,Tunnels,Active}`, `ops.RegisteredServer{ServerID,RemotePort,EnrolledAt}`, `config.Application{Name,Mappings}` (all existing).
- Produces: `contextCandidates([]ops.ContextInfo) []string`, `userCandidates([]ops.UserInfo, exclude []string) []string`, `serverCandidates([]ops.RegisteredServer) []string`, `appCandidates([]config.Application) []string` — each returns `"value\tdescription"` (or bare `"value"`) strings. Task 2 calls these.

- [ ] **Step 1: Write the failing tests**

`internal/cli/completions_test.go`:

```go
package cli

import (
	"reflect"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

func TestContextCandidates(t *testing.T) {
	got := contextCandidates([]ops.ContextInfo{
		{Name: "alice", ID: "a1b2c3d4", Role: "client", User: "alice", Relay: "relay.example"},
		{Name: "hq", ID: "deadbeef", Role: "relay", Relay: "relay.example"},
		{Name: "scratch"}, // empty ID → name entry only, no description
	})
	want := []string{
		"alice\tclient alice@relay.example",
		"a1b2c3d4\tid of alice",
		"hq\trelay@relay.example",
		"deadbeef\tid of hq",
		"scratch",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("contextCandidates =\n%q\nwant\n%q", got, want)
	}
}

func TestUserCandidates(t *testing.T) {
	users := []ops.UserInfo{
		{Name: "alice", Tunnels: []config.Tunnel{{}}, Active: true},
		{Name: "bob"},
	}
	got := userCandidates(users, nil)
	want := []string{
		"alice\t1 tunnel(s), applied",
		"bob\t0 tunnel(s), not applied",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("userCandidates =\n%q\nwant\n%q", got, want)
	}
	// apply's multi-arg form excludes names already on the command line.
	if got := userCandidates(users, []string{"alice"}); !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("userCandidates(exclude alice) = %q, want %q", got, want[1:])
	}
}

func TestServerCandidates(t *testing.T) {
	got := serverCandidates([]ops.RegisteredServer{
		{ServerID: "web-01-a1b2c3d4", RemotePort: 20000, EnrolledAt: "2026-07-01T10:30:00Z"},
		{ServerID: "old-1", RemotePort: 20001}, // pre-stamp entry: no enrolled date
	})
	want := []string{
		"web-01-a1b2c3d4\tport 20000, enrolled 2026-07-01T10:30:00Z",
		"old-1\tport 20001",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serverCandidates =\n%q\nwant\n%q", got, want)
	}
}

func TestAppCandidates(t *testing.T) {
	got := appCandidates([]config.Application{
		{Name: "web", Mappings: []config.PortMapping{{ClientPort: 8080, ServerPort: 80}}},
	})
	want := []string{"web\t1 mapping(s)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("appCandidates = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestContextCandidates|TestUserCandidates|TestServerCandidates|TestAppCandidates'`
Expected: FAIL (build error: `contextCandidates` etc. undefined).

- [ ] **Step 3: Write the builders**

`internal/cli/completions.go`:

```go
package cli

import (
	"fmt"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

// Completion candidates are "value\tdescription" strings (cobra's
// rich-completion format; zsh renders the description column). The builders
// are pure so they unit-test without a config dir. Everything completion
// reads is local — context index, users dir, server registry, config.yaml —
// never the relay or the daemon.

func contextCandidates(infos []ops.ContextInfo) []string {
	out := make([]string, 0, 2*len(infos))
	for _, c := range infos {
		desc := c.Role
		if c.User != "" {
			desc += " " + c.User
		}
		if c.Relay != "" {
			desc += "@" + c.Relay
		}
		entry := c.Name
		if d := strings.TrimSpace(desc); d != "" {
			entry += "\t" + d
		}
		out = append(out, entry)
		if c.ID != "" {
			out = append(out, c.ID+"\tid of "+c.Name)
		}
	}
	return out
}

func userCandidates(users []ops.UserInfo, exclude []string) []string {
	skip := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		skip[name] = true
	}
	out := make([]string, 0, len(users))
	for _, u := range users {
		if skip[u.Name] {
			continue
		}
		state := "not applied"
		if u.Active {
			state = "applied"
		}
		out = append(out, fmt.Sprintf("%s\t%d tunnel(s), %s", u.Name, len(u.Tunnels), state))
	}
	return out
}

func serverCandidates(servers []ops.RegisteredServer) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		desc := fmt.Sprintf("port %d", s.RemotePort)
		if s.EnrolledAt != "" {
			desc += ", enrolled " + s.EnrolledAt
		}
		out = append(out, s.ServerID+"\t"+desc)
	}
	return out
}

func appCandidates(apps []config.Application) []string {
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, fmt.Sprintf("%s\t%d mapping(s)", a.Name, len(a.Mappings)))
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestContextCandidates|TestUserCandidates|TestServerCandidates|TestAppCandidates' -v`
Expected: 4 × PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/completions.go internal/cli/completions_test.go
git commit -m "feat(cli): completion candidate builders"
```

---

### Task 2: Cobra glue and wiring

**Files:**
- Modify: `internal/cli/completions.go` (append glue funcs + `init()`)

**Interfaces:**
- Consumes: Task 1's builders; existing command vars `configUseContextCmd`, `configDeleteContextCmd`, `configExportCmd`, `configRenameContextCmd` (config.go), `deleteUserCmd` (delete_user.go), `editUserCmd` (edit_user.go), `exportUserCmd` (export_user.go), `applyUsersCmd`, `unregisterUserCmd` (apply_users.go), `relayUnenrollServerCmd` (relay_unenroll.go), `appEditCmd`, `appDeleteCmd` (app.go); `ops.New()`, `o.ListContexts()`, `o.ListUsers()`, `o.ListServers()`, `o.ListApplications()`.
- Produces: `ValidArgsFunction` set on the 12 commands above.

- [ ] **Step 1: Append the glue to `internal/cli/completions.go`**

Add `"github.com/spf13/cobra"` to the imports, then:

```go
// noComplete is the silent empty answer: no candidates, no file fallback.
// Tab completion must never surface an error.
func noComplete() ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func completeContexts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 { // only the first positional arg names a context
		return noComplete()
	}
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	infos, err := o.ListContexts()
	if err != nil {
		return noComplete()
	}
	return contextCandidates(infos), cobra.ShellCompDirectiveNoFileComp
}

func completeUsers(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return noComplete()
	}
	return listUserCandidates(nil)
}

// completeUsersMulti is for `apply [name...]`: every position completes,
// minus the names already typed.
func completeUsersMulti(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return listUserCandidates(args)
}

func listUserCandidates(exclude []string) ([]string, cobra.ShellCompDirective) {
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	users, err := o.ListUsers()
	if err != nil {
		return noComplete()
	}
	return userCandidates(users, exclude), cobra.ShellCompDirectiveNoFileComp
}

func completeServerIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return noComplete()
	}
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	servers, err := o.ListServers()
	if err != nil {
		return noComplete()
	}
	return serverCandidates(servers), cobra.ShellCompDirectiveNoFileComp
}

func completeApps(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return noComplete()
	}
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	return appCandidates(o.ListApplications()), cobra.ShellCompDirectiveNoFileComp
}

func init() {
	// Context selectors. rename-context's 2nd arg (the new name) is free
	// text — the len(args) guard in completeContexts leaves it uncompleted.
	configUseContextCmd.ValidArgsFunction = completeContexts
	configDeleteContextCmd.ValidArgsFunction = completeContexts
	configExportCmd.ValidArgsFunction = completeContexts
	configRenameContextCmd.ValidArgsFunction = completeContexts

	// Usernames.
	deleteUserCmd.ValidArgsFunction = completeUsers
	editUserCmd.ValidArgsFunction = completeUsers
	exportUserCmd.ValidArgsFunction = completeUsers
	unregisterUserCmd.ValidArgsFunction = completeUsers
	applyUsersCmd.ValidArgsFunction = completeUsersMulti

	// Server ids (local registry — no relay dial).
	relayUnenrollServerCmd.ValidArgsFunction = completeServerIDs

	// Application templates.
	appEditCmd.ValidArgsFunction = completeApps
	appDeleteCmd.ValidArgsFunction = completeApps
}
```

- [ ] **Step 2: Build, vet, full cli test run**

Run: `go build ./... && go vet ./... && go test ./internal/cli/`
Expected: clean build, `ok`.

- [ ] **Step 3: Smoke the `__complete` entry point**

```bash
make build
TW_CONFIG_DIR=$(mktemp -d) ./bin/tw __complete config use-context ""
```

Expected: no candidates (empty profile), a final directive line `:4` (NoFileComp), exit 0, nothing on stderr.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/completions.go
git commit -m "feat(cli): dynamic tab completion for contexts, users, servers, apps"
```

---

### Task 3: e2e assertions

**Files:**
- Modify: `e2e/e2e_test.go` (testContexts, after `name, id := row[1], row[2]`)
- Modify: `e2e/users_test.go` (testUserLifecycle, after the `tw server user list` alice assertion)
- Modify: `e2e/server_test.go` (testSecondTenant, after `server2ID, server2Port := row[1], row[2]`)

**Interfaces:**
- Consumes: harness helpers `execIn(t, service, script)`, `fatalf`; existing locals `name`, `id` (testContexts), `server2ID` (testSecondTenant).
- Produces: nothing downstream.

- [ ] **Step 1: testContexts — completion offers name AND short ID**

In `e2e/e2e_test.go`, directly after `name, id := row[1], row[2]`:

```go
	// Tab completion (cobra __complete = what zsh calls) offers both the
	// context name and its short ID for use-context.
	compOut := execIn(t, "admin", `tw __complete config use-context ""`)
	if !strings.Contains(compOut, name) || !strings.Contains(compOut, id) {
		fatalf(t, "use-context completion missing name %q or id %q:\n%s", name, id, compOut)
	}
```

Also add this check line to the testContexts `scenario(...)` call:

```go
		"tab completion: tw __complete config use-context offers the context name and its short ID",
```

- [ ] **Step 2: testUserLifecycle — completion offers the created user**

In `e2e/users_test.go`, directly after the `alice missing from user list` assertion:

```go
	// Tab completion offers the created user for user-selecting commands.
	compOut := execIn(t, "server", `tw __complete server user delete ""`)
	if !strings.Contains(compOut, "alice") {
		fatalf(t, "user delete completion does not offer alice:\n%s", compOut)
	}
```

Also add to its `scenario(...)` call:

```go
		"tab completion: tw __complete server user delete offers alice",
```

- [ ] **Step 3: testSecondTenant — completion offers the enrolled server-id**

In `e2e/server_test.go`, directly after `server2ID, server2Port := row[1], row[2]`:

```go
	// Tab completion offers the enrolled server-id for un-enroll-server.
	compOut := execIn(t, "admin", `tw __complete relay un-enroll-server ""`)
	if !strings.Contains(compOut, server2ID) {
		fatalf(t, "un-enroll-server completion does not offer %s:\n%s", server2ID, compOut)
	}
```

Also add to its `scenario(...)` call:

```go
		"tab completion: tw __complete relay un-enroll-server offers the enrolled server-id",
```

- [ ] **Step 4: Compile the e2e package**

Run: `go vet -tags e2e ./e2e/`
Expected: clean.

- [ ] **Step 5: Full e2e**

Run: `make e2e`
Expected: PASS (11 passed, Dashboard skipped), including the three new completion checks in the report table.

- [ ] **Step 6: Commit**

```bash
git add e2e/e2e_test.go e2e/users_test.go e2e/server_test.go
git commit -m "test(e2e): assert dynamic completion for contexts, users, server-ids"
```
