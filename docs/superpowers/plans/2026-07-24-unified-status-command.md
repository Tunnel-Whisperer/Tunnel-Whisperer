# Unified `tw status` Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A top-level, ungated `tw status` that prints the active context's identity (context name/ID, mode, user, relay, config path) followed by the mode-appropriate live status — one unified view with no repeated fields, shared by all four status commands.

**Architecture:** Extend `sharedStatus()` in `internal/cli/status.go` with a context-identity header (data read locally from `ops.ListContexts()` + `config.FilePath()`), remove the now-duplicated `Mode:` and relay `Domain:` lines from the two body printers, and register a new ungated root `status` command that calls the same `sharedStatus()`. Spec: `docs/superpowers/specs/2026-07-24-unified-status-command-design.md`.

**Tech Stack:** Go, cobra, existing `internal/ops` + `internal/config` packages; `go test`; docker-compose e2e suite in `e2e/`.

## Global Constraints

- Commit per task; concise imperative messages; no AI-attribution lines (user rule).
- `go test ./...` must pass after every task — note `internal/cli/coverage_test.go` fails the build if a new runnable command has no entry in `e2e/coverage.yaml`, so the coverage entry lands in the same task as the command.
- e2e output assertions that must keep passing: status output contains the relay domain (`relay_install_test.go:117`) and `Provisioned: true|false` (`resilience_test.go:106,127`).
- Match existing output style: two-space indent, aligned columns, `orDash` for empty values.

---

### Task 1: `statusHeaderLines` pure helper + unit test

**Files:**
- Modify: `internal/cli/status.go` (add helper at bottom, near `orDash`)
- Test: `internal/cli/status_test.go`

**Interfaces:**
- Consumes: `ops.ContextInfo{Name, ID, Role, User, Relay, Current string/bool}` (`internal/ops/context.go:21`), `orDash(string) string` (`internal/cli/status.go`).
- Produces: `statusHeaderLines(cur *ops.ContextInfo, total int, mode, configPath string) []string` — used by Task 2.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/status_test.go` (add `"github.com/tunnelwhisperer/tw/internal/ops"` to imports):

```go
func TestStatusHeaderLines(t *testing.T) {
	relayCtx := &ops.ContextInfo{Name: "relay-tw-test", ID: "bef98b84", Role: "relay", Relay: "relay.tw.test", Current: true}
	out := strings.Join(statusHeaderLines(relayCtx, 3, "relay", "/etc/tw/config.yaml"), "\n")
	for _, want := range []string{"relay-tw-test", "bef98b84", "3 stored", "Mode:     relay", "Relay:    relay.tw.test", "/etc/tw/config.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("relay header missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "User:") {
		t.Errorf("relay context must not print a User line:\n%s", out)
	}

	clientCtx := &ops.ContextInfo{Name: "alice", ID: "43c15a38", Role: "client", User: "alice", Relay: "relay.tw.test", Current: true}
	out = strings.Join(statusHeaderLines(clientCtx, 1, "client", "/etc/tw/config.yaml"), "\n")
	if !strings.Contains(out, "User:     alice") {
		t.Errorf("client header missing user line:\n%s", out)
	}

	out = strings.Join(statusHeaderLines(nil, 0, "", "/etc/tw/config.yaml"), "\n")
	if !strings.Contains(out, "not set up") {
		t.Errorf("empty header missing not-set-up hint:\n%s", out)
	}
	if !strings.Contains(out, "Mode:     —") {
		t.Errorf("empty header must show a dash mode:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestStatusHeaderLines -v`
Expected: FAIL — `undefined: statusHeaderLines` (compile error).

- [ ] **Step 3: Write minimal implementation**

Add to `internal/cli/status.go` above `orDash`:

```go
// statusHeaderLines renders the identity header shown by every status command:
// which context is active, its mode, user, relay, and the config file path.
// cur is nil when no context exists yet (machine not set up). Pure function so
// the layout is unit-testable without a daemon or config dir.
func statusHeaderLines(cur *ops.ContextInfo, total int, mode, configPath string) []string {
	var lines []string
	if cur == nil {
		lines = append(lines, "  Context:  — (not set up yet)")
	} else {
		lines = append(lines, fmt.Sprintf("  Context:  %s (%s) — %d stored", cur.Name, orDash(cur.ID), total))
	}
	lines = append(lines, fmt.Sprintf("  Mode:     %s", orDash(mode)))
	if cur != nil && cur.User != "" {
		lines = append(lines, fmt.Sprintf("  User:     %s", cur.User))
	}
	if cur != nil && cur.Relay != "" {
		lines = append(lines, fmt.Sprintf("  Relay:    %s", cur.Relay))
	}
	lines = append(lines, fmt.Sprintf("  Config:   %s", configPath))
	return lines
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestStatusHeaderLines -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/status.go internal/cli/status_test.go
git commit -m "feat(cli): add status header formatter for unified status"
```

---

### Task 2: Wire the header into `sharedStatus`, add ungated root `tw status`

**Files:**
- Modify: `internal/cli/status.go` (`sharedStatus`, `runStatusRemote`, `runStatusLocal`, `init`)
- Modify: `e2e/coverage.yaml` (new `"status"` entry — required or `go test ./...` fails)
- Modify: `docs/reference/cli.md:14` (status row description)

**Interfaces:**
- Consumes: `statusHeaderLines` (Task 1), `ops.New() (*ops.Ops, error)`, `(*ops.Ops).ListContexts() ([]ops.ContextInfo, error)`, `config.Load()`, `config.FilePath() string`.
- Produces: root command `tw status` (no `requireMode` call); `printStatusHeader()` used only inside `status.go`.

- [ ] **Step 1: Add `printStatusHeader` and call it from `sharedStatus`**

In `internal/cli/status.go`, add below `sharedStatus`:

```go
// printStatusHeader prints the context identity header. Context data is local
// state, so it is read from disk even when the daemon answers the rest.
// Best-effort: a half-initialized profile still gets a header.
func printStatusHeader() {
	cfg, _ := config.Load()
	mode := ""
	if cfg != nil {
		mode = cfg.Mode
	}
	var cur *ops.ContextInfo
	total := 0
	if o, err := ops.New(); err == nil {
		if list, lerr := o.ListContexts(); lerr == nil {
			total = len(list)
			for i := range list {
				if list[i].Current {
					cur = &list[i]
					break
				}
			}
		}
	}
	for _, l := range statusHeaderLines(cur, total, mode, config.FilePath()) {
		fmt.Println(l)
	}
	fmt.Println()
}
```

Change `sharedStatus` to print it first:

```go
func sharedStatus() error {
	printStatusHeader()
	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	client, err := api.Dial(addr)
	if err != nil {
		return runStatusLocal()
	}
	defer client.Close()
	return runStatusRemote(client)
}
```

- [ ] **Step 2: De-duplicate the body printers**

In `runStatusRemote`, delete the `Mode:` line and the relay `Domain:` line, keeping the rest:

```go
	fmt.Printf("  Users:  %d\n", resp.UserCount)
	fmt.Println()

	fmt.Println("  Relay:")
	fmt.Printf("    Provisioned: %v\n", resp.Relay.Provisioned)
	if resp.Relay.Provisioned {
		fmt.Printf("    IP:          %s\n", resp.Relay.IP)
		fmt.Printf("    Provider:    %s\n", resp.Relay.Provider)
	}
```

In `runStatusLocal`, same removals, plus an early return for a not-set-up machine (header already said `Mode: —`):

```go
	mode := o.Mode()
	if mode == "" {
		fmt.Println("  (not set up yet — create a relay, join one as a server, or import a client bundle)")
		return nil
	}
	relay := o.GetRelayStatus()
	users, _ := o.ListUsers()

	fmt.Printf("  Users:  %d\n", len(users))
	fmt.Println()

	fmt.Println("  Relay:")
	fmt.Printf("    Provisioned: %v\n", relay.Provisioned)
	if relay.Provisioned {
		fmt.Printf("    IP:          %s\n", relay.IP)
		fmt.Printf("    Provider:    %s\n", relay.Provider)
	}
```

(Keep the trailing `if mode == "server" || mode == "client"` daemon hint unchanged.)

- [ ] **Step 3: Add the ungated root command**

In `internal/cli/status.go`, above the existing role variants:

```go
// statusCmd is the top-level, ungated status: it detects the active context's
// mode and prints the same unified view the role-scoped variants show. No
// requireMode gate — this is the "what is going on here?" entry point that
// must work on any machine, set up or not.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show overall status: active context, mode, and its live status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sharedStatus()
	},
}
```

And register it in `init()`:

```go
func init() {
	rootCmd.AddCommand(statusCmd)
	relayCmd.AddCommand(relayStatusCmd)
	serverCmd.AddCommand(serverStatusCmd)
	clientCmd.AddCommand(clientStatusCmd)
}
```

- [ ] **Step 4: Declare e2e coverage for the new command**

In `e2e/coverage.yaml`, after the `"config view"` line, add:

```yaml
  "status":                  [TestE2E/Contexts]
```

(The actual e2e exercise lands in Task 3; this entry satisfies `TestEveryCommandHasE2ECoverage` now.)

- [ ] **Step 5: Update the CLI reference row**

In `docs/reference/cli.md`, replace the `tw status` row:

```markdown
| `tw status` | any | Overall status: active context (name/ID), mode, relay, config path, then that mode's live status. Ungated — works on any machine. |
```

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS (including `TestEveryCommandHasE2ECoverage` and `TestDaemonContextMismatch`).

Also sanity-run the binary against a temp dir:

Run: `go run ./cmd/tw --config-dir "$(mktemp -d)" status`
Expected: header with `Context:  — (not set up yet)`, `Mode:     —`, `Config:` path, then the not-set-up hint; exit 0.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/status.go e2e/coverage.yaml docs/reference/cli.md
git commit -m "feat(cli): ungated top-level tw status with unified context header"
```

---

### Task 3: e2e coverage in the Contexts scenario

**Files:**
- Modify: `e2e/e2e_test.go` (`testContexts`, after the client get-contexts regexp assertion)

**Interfaces:**
- Consumes: `execIn(t, service, cmd string) string`, `fatalf`, the scenario brief list in `testContexts`, containers `admin` (mode relay) and `client` (mode client, user alice).
- Produces: e2e assertions backing the `"status": [TestE2E/Contexts]` coverage entry.

- [ ] **Step 1: Extend the scenario brief**

In `testContexts`'s `scenario(...)` call, add one brief line:

```go
		"tw status (ungated) prints the unified header — context, mode, and USER alice on the client; context and mode relay on the admin",
```

- [ ] **Step 2: Add the assertions**

After the client `get-contexts` regexp check in `testContexts`:

```go
	// The ungated top-level status must show the unified header on any role.
	statusOut := execIn(t, "admin", "tw status")
	if !strings.Contains(statusOut, "Context:") || !strings.Contains(statusOut, "Mode:     relay") {
		fatalf(t, "admin tw status header missing context/mode:\n%s", statusOut)
	}
	statusOut = execIn(t, "client", "tw status")
	if !strings.Contains(statusOut, "Mode:     client") || !strings.Contains(statusOut, "User:     alice") {
		fatalf(t, "client tw status header missing mode/user:\n%s", statusOut)
	}
```

- [ ] **Step 3: Verify the e2e suite compiles**

Run: `go vet ./e2e/`
Expected: clean. (Running the full docker e2e suite is the user's call — note it in the final report; do not silently skip it.)

- [ ] **Step 4: Commit**

```bash
git add e2e/e2e_test.go
git commit -m "test(e2e): assert ungated tw status header in Contexts scenario"
```
