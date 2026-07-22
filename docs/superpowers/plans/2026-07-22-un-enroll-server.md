# `tw relay un-enroll-server` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `tw relay un-enroll-server <server-id>` that removes an enrolled tenant from the relay completely — config AND live connections.

**Architecture:** Mirror of `ops.EnrollServer`: re-render every relay artifact (authorized_keys, Caddyfile, xray config.json) from the remaining tenant list, hot-remove the tenant's running xray inbound/rules via the existing gRPC channel, kill its reverse-tunnel sshd session by listener port, then delete the local registry entry last so mid-way failures are retryable.

**Tech Stack:** Go 1.26, xray-core gRPC (`proxyman/command`, `router/command`), Cobra CLI, Docker-Compose e2e suite.

**Spec:** `docs/superpowers/specs/2026-07-22-un-enroll-server-design.md` — read it first.

## Global Constraints

- **NO git commits anywhere in this plan** — the user drives git. Skip every "commit" convention; leave the tree dirty.
- Verify = `go build ./...` + `go vet ./...` pass; touched-package unit tests pass; full `make e2e` passes at the end (CLAUDE.md done-criteria).
- Module path `github.com/tunnelwhisperer/tw`; own ssh/xray packages import as `twssh`/`twxray`; errors wrapped `fmt.Errorf("...: %w", err)`.
- Internal role/mode string stays `"admin"`; the command group is `tw relay`.
- After the final task, build BOTH bins (`make build`, `make build-windows`) and stage `bin/tw.exe` to `/mnt/c/Users/alial/Downloads/tw.exe`.

---

### Task 1: `liveRemoveTenant` — hot-remove a tenant from the running relay xray

**Files:**
- Modify: `internal/ops/relaygrpc.go` (append after `liveAddTenant`, line 74)

**Interfaces:**
- Consumes: `dialRelayGRPC(client *gossh.Client)` (already in `internal/ops/user.go`), tags rendered by `internal/relay/xray/tenant.go`: inbound `vless-in-<ServerID>`, rules `allow-<ServerID>` / `deny-<ServerID>`.
- Produces: `func liveRemoveTenant(client *gossh.Client, serverID string) error` — used by Task 4.

No unit test is possible (requires a live xray gRPC endpoint); the e2e SecondTenant extension (Task 6) is the test.

- [ ] **Step 1: Implement**

Append to `internal/ops/relaygrpc.go` (imports needed beyond existing: `strings`, `log/slog`):

```go
// liveRemoveTenant removes one tenant's routing rules and inbound from the
// relay's running Xray process via gRPC, without restarting Xray — the
// counterpart of liveAddTenant. Removing the inbound severs the tenant's
// established VLESS sessions (its server transport and all its clients).
//
// Call order: rules first (they reference the inbound's tag), then the
// inbound. "not found" errors are tolerated and logged — the tenant may
// already be gone live after an earlier partial un-enroll — so re-running
// is idempotent. Any other gRPC error fails the call.
func liveRemoveTenant(client *gossh.Client, serverID string) error {
	conn, err := dialRelayGRPC(client)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rs := routercmd.NewRoutingServiceClient(conn)
	for _, ruleTag := range []string{"allow-" + serverID, "deny-" + serverID} {
		if _, err := rs.RemoveRule(ctx, &routercmd.RemoveRuleRequest{RuleTag: ruleTag}); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				slog.Warn("relay xray rule already gone", "ruleTag", ruleTag)
				continue
			}
			return fmt.Errorf("RemoveRule %s: %w", ruleTag, err)
		}
	}

	hs := proxymanCmd.NewHandlerServiceClient(conn)
	inboundTag := "vless-in-" + serverID
	if _, err := hs.RemoveInbound(ctx, &proxymanCmd.RemoveInboundRequest{Tag: inboundTag}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			slog.Warn("relay xray inbound already gone", "tag", inboundTag)
			return nil
		}
		return fmt.Errorf("RemoveInbound %s: %w", inboundTag, err)
	}
	return nil
}
```

(`RemoveInboundRequest.Tag` and `RemoveRuleRequest.RuleTag` verified against the pinned xray-core `v1.260327.1-0.20260617150841`.)

- [ ] **Step 2: Verify it compiles**

Run: `go build ./... && go vet ./internal/ops/`
Expected: no output. (`liveRemoveTenant` is unused until Task 4 — Go allows unused package-level funcs.)

---

### Task 2: Extract `relayTenantState` (shared by enroll and un-enroll)

**Files:**
- Modify: `internal/ops/enroll.go:66-127` (EnrollServer step 2)

**Interfaces:**
- Produces: `func (o *Ops) relayTenantState(registered []RegisteredServer) ([]caddy.Server, []relayxray.Tenant, map[string][]byte, error)` — admin's own entry first, then one entry per element of `registered`. Used by Task 4.

Behavior-preserving refactor; existing tests + e2e guard it.

- [ ] **Step 1: Add the helper**

In `internal/ops/enroll.go`, above `EnrollServer`:

```go
// relayTenantState builds the relay-side tenant state — Caddy server blocks,
// xray tenants, and the CA certs to install — from the admin's own entry
// plus the given registered servers.
func (o *Ops) relayTenantState(registered []RegisteredServer) ([]caddy.Server, []relayxray.Tenant, map[string][]byte, error) {
	cfg := o.Config()
	osHost, _ := os.Hostname()
	adminID := deriveServerID(osHost, cfg.Xray.UUID)
	adminRemotePort := cfg.Server.RemotePort

	adminCAPEM, err := os.ReadFile(config.CACertPath())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading admin CA cert: %w", err)
	}

	servers := []caddy.Server{{
		ID:         adminID,
		Path:       "/tw/" + adminID,
		CACertPath: fmt.Sprintf("/etc/caddy/ca/%s.crt", adminID),
		Upstream:   fmt.Sprintf("h2c://127.0.0.1:%d", adminRemotePort+10000),
		Role:       "admin",
	}}
	tenants := []relayxray.Tenant{{
		ServerID:   adminID,
		UUID:       cfg.Xray.UUID,
		RemotePort: adminRemotePort,
	}}
	caCerts := map[string][]byte{adminID: adminCAPEM}

	for _, s := range registered {
		servers = append(servers, caddy.Server{
			ID:         s.ServerID,
			Path:       "/tw/" + s.ServerID,
			CACertPath: fmt.Sprintf("/etc/caddy/ca/%s.crt", s.ServerID),
			Upstream:   fmt.Sprintf("h2c://127.0.0.1:%d", s.RemotePort+10000),
			Role:       "server",
		})
		tenants = append(tenants, relayxray.Tenant{
			ServerID:   s.ServerID,
			UUID:       s.UUID,
			RemotePort: s.RemotePort,
		})
		caCerts[s.ServerID] = []byte(s.CACertPEM)
	}
	return servers, tenants, caCerts, nil
}
```

- [ ] **Step 2: Rewrite EnrollServer's step 2 to use it**

Replace the body between the step-2 `running` progress event and the step-2 `completed` event (currently: adminID/adminRemotePort derivation, adminCAPEM read, seed slices, `ListServers` loop) with:

```go
	allServers, err := o.ListServers()
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("listing registered servers: %w", err)
	}
	servers, tenants, caCerts, err := o.relayTenantState(allServers)
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "failed", Error: err.Error()})
		return nil, err
	}
```

Keep everything else identical — `cfg := o.Config()` stays (used later for `cfg.Xray.RelayHost` / `cfg.Server.RelaySSHUser`), `newTenant := ...` stays, the `completed` event's message (`len(tenants)`, `len(allServers)`) stays.

- [ ] **Step 3: Verify**

Run: `go build ./... && go vet ./internal/ops/ && go test ./internal/ops/`
Expected: build/vet clean, all ops tests PASS.

---

### Task 3: `killRelayListenerCmd` — kill-by-listener-port shell command (TDD)

**Files:**
- Create: `internal/ops/unenroll.go`
- Create: `internal/ops/unenroll_test.go`

**Interfaces:**
- Produces: `func killRelayListenerCmd(port int) string` — a POSIX-shell one-liner for `runRelayCmd`. Used by Task 4.

Rationale (from spec): the tenant's established reverse-tunnel sshd session survives the authorized_keys rewrite and holds the LISTEN socket on `127.0.0.1:<RemotePort>`. All tenants AND the admin's own management session share one SSH user, so kill by port, never by process name. Pure coreutils + awk — no `ss`/`fuser` on the minimal e2e relay image.

- [ ] **Step 1: Write the failing test**

Create `internal/ops/unenroll_test.go`:

```go
package ops

import (
	"strings"
	"testing"
)

func TestKillRelayListenerCmd(t *testing.T) {
	cmd := killRelayListenerCmd(20001)
	// 20001 = 0x4E21 — the /proc/net/tcp local_address port is hex.
	if !strings.Contains(cmd, `/:4E21$/`) {
		t.Errorf("command does not match hex port 4E21:\n%s", cmd)
	}
	if !strings.Contains(cmd, "/proc/net/tcp") || !strings.Contains(cmd, "/proc/net/tcp6") {
		t.Errorf("command must read /proc/net/tcp and /proc/net/tcp6:\n%s", cmd)
	}
	// No listener found must still exit 0 (tunnel already down = success).
	if !strings.HasSuffix(cmd, "true") {
		t.Errorf("command must end in 'true' so no-match is not an error:\n%s", cmd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ops/ -run TestKillRelayListenerCmd`
Expected: FAIL — `undefined: killRelayListenerCmd`

- [ ] **Step 3: Implement**

Create `internal/ops/unenroll.go`:

```go
package ops

import (
	"fmt"
)

// killRelayListenerCmd returns a shell command that kills whatever process
// holds a LISTEN socket on the given port on the relay — in practice the
// sshd session serving a tenant's reverse tunnel, which survives an
// authorized_keys rewrite. All tenants (and the admin's own management
// session) share one SSH user, so the kill targets the listener port, never
// a process name. Pure coreutils + awk (no ss/fuser): find the LISTEN
// socket inode in /proc/net/tcp{,6} (state 0A, hex port), then the pid
// whose fd table holds that inode. No listener found is success — the
// tunnel was already down — hence the trailing true.
func killRelayListenerCmd(port int) string {
	return fmt.Sprintf(
		`inos=$(awk '$4=="0A" && $2 ~ /:%04X$/ {print $10}' /proc/net/tcp /proc/net/tcp6); `+
			`for ino in $inos; do for p in /proc/[0-9]*; do `+
			`sudo ls -l "$p/fd" 2>/dev/null | grep -q "socket:\[$ino\]" && sudo kill "${p#/proc/}"; `+
			`done; done; true`, port)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ops/ -run TestKillRelayListenerCmd`
Expected: PASS

---

### Task 4: `ops.UnenrollServer` (TDD on the pure filter)

**Files:**
- Modify: `internal/ops/unenroll.go` (from Task 3)
- Modify: `internal/ops/unenroll_test.go` (from Task 3)

**Interfaces:**
- Consumes: `o.ListServers()`, `o.relayTenantState(...)` (Task 2), `liveRemoveTenant(client, serverID)` (Task 1), `killRelayListenerCmd(port)` (Task 3), `renderRelayAuthorizedKeys(adminPubKey, servers)` (`internal/ops/enroll.go:29`), `runRelayCmd(client, cmd)` (`internal/ops/relay.go:727`), `o.RelaySSH(func(*gossh.Client) error)`, `caddy.RenderCaddyfile`, `relayxray.RenderConfig`, `RegistryDir()`, `ProgressFunc`/`ProgressEvent`.
- Produces: `func (o *Ops) UnenrollServer(serverID string, progress ProgressFunc) error` and `func excludeServer(servers []RegisteredServer, serverID string) []RegisteredServer`. Used by Task 5 (CLI).

- [ ] **Step 1: Write the failing test for the filter**

Append to `internal/ops/unenroll_test.go`:

```go
func TestExcludeServer(t *testing.T) {
	in := []RegisteredServer{
		{ServerID: "a-1", RemotePort: 20000},
		{ServerID: "b-2", RemotePort: 20001},
		{ServerID: "c-3", RemotePort: 20002},
	}
	out := excludeServer(in, "b-2")
	if len(out) != 2 || out[0].ServerID != "a-1" || out[1].ServerID != "c-3" {
		t.Errorf("excludeServer(b-2) = %+v, want a-1 and c-3 in order", out)
	}
	if got := excludeServer(in, "nope"); len(got) != 3 {
		t.Errorf("excluding an absent id must keep all %d servers, got %d", len(in), len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ops/ -run TestExcludeServer`
Expected: FAIL — `undefined: excludeServer`

- [ ] **Step 3: Implement excludeServer + UnenrollServer**

Append to `internal/ops/unenroll.go` (imports become: `encoding/base64`, `fmt`, `os`, `path/filepath`, `github.com/tunnelwhisperer/tw/internal/config`, `github.com/tunnelwhisperer/tw/internal/relay/caddy`, `relayxray "github.com/tunnelwhisperer/tw/internal/relay/xray"`, `gossh "golang.org/x/crypto/ssh"`):

```go
// excludeServer returns servers without the entry matching serverID,
// preserving order.
func excludeServer(servers []RegisteredServer, serverID string) []RegisteredServer {
	out := make([]RegisteredServer, 0, len(servers))
	for _, s := range servers {
		if s.ServerID != serverID {
			out = append(out, s)
		}
	}
	return out
}

// UnenrollServer removes an enrolled server from the relay completely:
// config (authorized_keys line, xray inbound/rules, Caddyfile handle, CA
// cert, registry entry) AND live state (established VLESS sessions, the
// reverse-tunnel sshd session). Order: block re-auth first, then drop live
// state, then clean files, then forget locally — a mid-way failure leaves
// the registry entry intact, and every step is idempotent, so re-running
// the command retries cleanly.
func (o *Ops) UnenrollServer(serverID string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}
	const total = 4

	// Step 1: Resolve the target in the registry.
	progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "running"})
	all, err := o.ListServers()
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "failed", Error: err.Error()})
		return fmt.Errorf("listing registered servers: %w", err)
	}
	var target *RegisteredServer
	for i := range all {
		if all[i].ServerID == serverID {
			target = &all[i]
		}
	}
	if target == nil {
		err := fmt.Errorf("server %q is not enrolled", serverID)
		progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "failed", Error: err.Error()})
		return err
	}
	remaining := excludeServer(all, serverID)
	progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "completed",
		Message: fmt.Sprintf("%s on port %d", target.ServerID, target.RemotePort)})

	// Step 2: Render everything from the REMAINING tenant list (full-rewrite
	// philosophy, same as enroll) — outside the SSH callback, fail fast.
	progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "running"})
	cfg := o.Config()
	servers, tenants, _, err := o.relayTenantState(remaining)
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return err
	}
	caddyfile, err := caddy.RenderCaddyfile(caddy.Config{Domain: cfg.Xray.RelayHost, Servers: servers})
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return fmt.Errorf("rendering Caddyfile: %w", err)
	}
	xjson, err := relayxray.RenderConfig(relayxray.Config{Tenants: tenants})
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return fmt.Errorf("rendering relay Xray config: %w", err)
	}
	adminPubKey, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return fmt.Errorf("reading admin public key: %w", err)
	}
	akContent := renderRelayAuthorizedKeys(string(adminPubKey), remaining)
	sshUser := cfg.Server.RelaySSHUser
	progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "completed",
		Message: fmt.Sprintf("%d tenants remain (admin + %d servers)", len(tenants), len(remaining))})

	// Step 3: Apply on the relay — ONE SSH connection for everything, so no
	// step after the Caddy reload needs a fresh TLS handshake.
	progress(ProgressEvent{Step: 3, Total: total, Label: "Remove from relay", Status: "running"})
	err = o.RelaySSH(func(client *gossh.Client) error {
		// (a) Block re-auth: rewrite authorized_keys without the target.
		akPath := fmt.Sprintf("/home/%s/.ssh/authorized_keys", sshUser)
		akB64 := base64.StdEncoding.EncodeToString([]byte(akContent))
		writeKeys := fmt.Sprintf(
			"echo %s | base64 -d | sudo tee %s >/dev/null && sudo chown %s:%s %s && sudo chmod 600 %s",
			akB64, akPath, sshUser, sshUser, akPath, akPath,
		)
		if err := runRelayCmd(client, writeKeys); err != nil {
			return fmt.Errorf("writing authorized_keys: %w", err)
		}

		// (b) Hot-remove the running inbound + rules; severs the tenant's
		// established VLESS sessions (server transport and all its clients).
		if err := liveRemoveTenant(client, serverID); err != nil {
			return err
		}

		// (c) Kill the reverse-tunnel sshd session still holding the
		// listener on the tenant's port.
		if err := runRelayCmd(client, killRelayListenerCmd(target.RemotePort)); err != nil {
			return fmt.Errorf("killing reverse-tunnel session: %w", err)
		}

		// (d) Rewrite the Caddyfile, validate, graceful reload; drop the CA.
		cfB64 := base64.StdEncoding.EncodeToString([]byte(caddyfile))
		if err := runRelayCmd(client, fmt.Sprintf("echo %s | base64 -d | sudo tee /etc/caddy/Caddyfile >/dev/null", cfB64)); err != nil {
			return fmt.Errorf("writing Caddyfile: %w", err)
		}
		if err := runRelayCmd(client, "sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile"); err != nil {
			return fmt.Errorf("relay Caddyfile failed validation (not reloaded): %w", err)
		}
		if err := runRelayCmd(client, "sudo systemctl reload caddy"); err != nil {
			return fmt.Errorf("reloading caddy: %w", err)
		}
		if err := runRelayCmd(client, fmt.Sprintf("sudo rm -f /etc/caddy/ca/%s.crt", serverID)); err != nil {
			return fmt.Errorf("removing CA cert: %w", err)
		}

		// (e) Persist the xray config for restarts/reboots (the running
		// process was already updated in (b) — no restart).
		xB64 := base64.StdEncoding.EncodeToString([]byte(xjson))
		if err := runRelayCmd(client, fmt.Sprintf("echo %s | base64 -d | sudo tee /usr/local/etc/xray/config.json >/dev/null", xB64)); err != nil {
			return fmt.Errorf("writing relay Xray config: %w", err)
		}
		return nil
	})
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Remove from relay", Status: "failed", Error: err.Error()})
		return fmt.Errorf("removing from relay: %w", err)
	}
	progress(ProgressEvent{Step: 3, Total: total, Label: "Remove from relay", Status: "completed",
		Message: "config removed, live connections killed"})

	// Step 4: Forget locally — LAST, so any earlier failure keeps the entry
	// and the command can simply be re-run.
	progress(ProgressEvent{Step: 4, Total: total, Label: "Remove from registry", Status: "running"})
	if err := os.Remove(filepath.Join(RegistryDir(), serverID+".json")); err != nil {
		progress(ProgressEvent{Step: 4, Total: total, Label: "Remove from registry", Status: "failed", Error: err.Error()})
		return fmt.Errorf("removing registry entry: %w", err)
	}
	progress(ProgressEvent{Step: 4, Total: total, Label: "Remove from registry", Status: "completed",
		Message: fmt.Sprintf("%s un-enrolled", serverID)})
	return nil
}
```

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/ops/ && go build ./... && go vet ./internal/ops/`
Expected: all PASS (TestExcludeServer, TestKillRelayListenerCmd, and all pre-existing), build/vet clean.

---

### Task 5: CLI command + coverage entry

**Files:**
- Create: `internal/cli/relay_unenroll.go`
- Modify: `e2e/coverage.yaml` (after the `"relay enroll-server"` line)

**Interfaces:**
- Consumes: `ops.UnenrollServer` and `ops.RegisteredServer` (Task 4), `requireMode`, `cliProgress`, `relayCmd`.
- Produces: user-facing `tw relay un-enroll-server <server-id> [--yes]`.

- [ ] **Step 1: Create the command**

Create `internal/cli/relay_unenroll.go`:

```go
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var unenrollYes bool

var relayUnenrollServerCmd = &cobra.Command{
	Use:   "un-enroll-server <server-id>",
	Short: "Un-enroll a server from the relay and kill its live connections",
	Args:  cobra.ExactArgs(1),
	RunE:  runRelayUnenrollServer,
}

func init() {
	relayUnenrollServerCmd.Flags().BoolVar(&unenrollYes, "yes", false, "skip the confirmation prompt")
	relayCmd.AddCommand(relayUnenrollServerCmd)
}

func runRelayUnenrollServer(cmd *cobra.Command, args []string) error {
	if err := requireMode("admin"); err != nil {
		return err
	}
	serverID := args[0]
	o, err := ops.New()
	if err != nil {
		return err
	}
	servers, err := o.ListServers()
	if err != nil {
		return err
	}
	var target *ops.RegisteredServer
	for i := range servers {
		if servers[i].ServerID == serverID {
			target = &servers[i]
		}
	}
	if target == nil {
		return fmt.Errorf("server %q is not enrolled", serverID)
	}

	if !unenrollYes {
		enrolled := "-"
		if t, err := time.Parse(time.RFC3339, target.EnrolledAt); err == nil {
			enrolled = t.Format("2006-01-02T15:04")
		}
		fmt.Printf("\n  Server:   %s\n  Port:     %d\n  Enrolled: %s\n\n", target.ServerID, target.RemotePort, enrolled)
		fmt.Print("  Un-enroll this server? Its relay access and all its live connections end immediately. [y/N]: ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" {
			fmt.Println("  Aborted.")
			return nil
		}
		fmt.Println()
	}

	if err := o.UnenrollServer(serverID, cliProgress); err != nil {
		return err
	}
	fmt.Printf("\n  Un-enrolled %s. Run 'tw server join' on it to re-enroll.\n", serverID)
	return nil
}
```

(Prompt style mirrors `internal/cli/destroy_relay.go:68`; details display mirrors the get-servers ENROLLED formatting in `internal/cli/relay_enroll.go:82-84`.)

- [ ] **Step 2: Map the command in coverage.yaml**

In `e2e/coverage.yaml`, after the `"relay enroll-server"` line, add (match the file's existing alignment style):

```yaml
  "relay un-enroll-server":        [TestE2E/SecondTenant]
```

- [ ] **Step 3: Verify build + coverage gate**

Run: `go build ./... && go vet ./internal/cli/ && go test ./internal/cli/ -run TestEveryCommandHasE2ECoverage`
Expected: PASS — the new command is mapped to a real scenario. (The scenario assertion itself lands in Task 6; this gate only checks the mapping exists and points at a real test name.)

Also smoke the wiring: `go run ./cmd/tw relay un-enroll-server --help`
Expected: usage text with the `--yes` flag.

---

### Task 6: e2e — un-enroll a LIVE tenant in SecondTenant

**Files:**
- Modify: `e2e/server_test.go:126-202` (`testSecondTenant`)

**Interfaces:**
- Consumes: harness helpers `execIn` / `execInOK` / `killMatching` / `fatalf` / `scenario` (`e2e/harness.go`), `localCertsShim` (`e2e/relay_install_test.go:34`). The relay container is compose service `"relay"`.
- Produces: e2e proof for `relay un-enroll-server` (the coverage.yaml mapping from Task 5).

Key harness fact: un-enroll re-renders the relay Caddyfile, which wipes the e2e `local_certs` shim — reapply `localCertsShim(t)` right after the command, before anything makes a fresh TLS connection. The un-enroll command itself is safe to run synchronously: it uses ONE SSH connection established before the reload, so no step inside it needs a new handshake (unlike enroll's detached + mid-run shim dance).

- [ ] **Step 1: Extend the scenario header**

In `testSecondTenant`, add two check lines to the `scenario(...)` call after the existing last check:

```go
		"tw relay un-enroll-server --yes removes the LIVE server2: registry row gone, relay listener gone, its tunnel test fails",
		"server-1 and the admin remain unaffected after the un-enroll (non-disruptive removal)")
```

(Adjust the previous last line's trailing `)` accordingly.)

- [ ] **Step 2: Capture server2's ID and port from the get-servers table**

Replace the existing server2 "down" assertion (`e2e/server_test.go:179-181`) with a capturing version:

```go
	row := regexp.MustCompile(`(?m)^(` + host + `\S*)\s+/tw/` + host + `\S*\s+(\d+)\s+\S+\s+down\s*$`).FindStringSubmatch(regOut)
	if row == nil {
		fatalf(t, "get-servers does not show server2 (%s-*) with its path and TUNNEL down:\n%s", host, regOut)
	}
	server2ID, server2Port := row[1], row[2]
```

- [ ] **Step 3: Append the un-enroll block at the end of testSecondTenant**

After the existing step-5 admin `tw relay test` assertion (`e2e/server_test.go:198-201`), append:

```go
	// 6. Un-enroll server2 while its daemon runs and its tunnel is LIVE —
	// removal must be total: config gone AND live connections killed. The
	// --yes flag skips the confirmation prompt (no TTY here).
	out = execIn(t, "admin", "tw relay un-enroll-server "+server2ID+" --yes")
	if !strings.Contains(out, "Un-enrolled "+server2ID) {
		fatalf(t, "un-enroll did not report success:\n%s", out)
	}
	// The un-enroll re-rendered the Caddyfile, wiping the local_certs shim —
	// reapply before anything opens a fresh TLS connection to the relay.
	localCertsShim(t)

	// The reverse-tunnel listener is gone: no LISTEN (state 0A) row on
	// server2's port in the relay's /proc/net/tcp{,6}. This proves the kill,
	// not just the config removal — the sshd session would survive the
	// authorized_keys rewrite alone.
	portN, err := strconv.Atoi(server2Port)
	if err != nil {
		fatalf(t, "unparseable PORT column %q", server2Port)
	}
	proc := execIn(t, "relay", "cat /proc/net/tcp /proc/net/tcp6")
	if regexp.MustCompile(fmt.Sprintf(`(?m)^\s*\d+: [0-9A-F]+:%04X\s+\S+\s+0A\b`, portN)).MatchString(proc) {
		fatalf(t, "relay still LISTENs on server2's port %s after un-enroll:\n%s", server2Port, proc)
	}

	// The registry no longer lists it.
	regOut = execIn(t, "admin", "tw relay get-servers")
	if regexp.MustCompile(`(?m)^` + host).MatchString(regOut) {
		fatalf(t, "get-servers still lists server2 after un-enroll:\n%s", regOut)
	}

	// server2 is dark: a fresh tunnel test must FAIL (its inbound and its CA
	// trust are gone from the relay).
	if testOut, testErr := execInOK("server2", "tw server test"); testErr == nil && strings.Contains(testOut, "tunnel and shell working") {
		fatalf(t, "server2 tunnel still works after un-enroll:\n%s", testOut)
	}
	killMatching(t, "server2", "tw server start") // stop its reconnect spam

	// 7. Still non-disruptive: server-1 and the admin are unaffected.
	out = execIn(t, "server", "tw server test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "server-1 tunnel broken after server2 un-enroll:\n%s", out)
	}
	out = execIn(t, "admin", "tw relay test")
	if !strings.Contains(out, "tunnel and shell working") {
		fatalf(t, "admin tunnel broken after server2 un-enroll:\n%s", out)
	}
```

Add `"fmt"` and `"strconv"` to `e2e/server_test.go`'s imports if not already present.

Also update the `testSecondTenant` doc comment (line 121-125) — it now also proves total removal:

```go
// testSecondTenant enrolls a SECOND server (the relay's third tenant, after
// the admin and server-1), proves the enrollment is live and non-disruptive,
// then UN-enrolls it while its tunnel is live and proves the removal is
// total (registry row gone, relay listener killed, fresh tunnel test fails)
// and equally non-disruptive to the remaining tenants. Tenant ISOLATION
// (server A's client cannot reach server B) is still deferred.
```

- [ ] **Step 4: Compile-check the e2e package**

Run: `go vet -tags e2e ./e2e/`
Expected: clean.

- [ ] **Step 5: Run the full e2e suite**

Run: `make e2e` (from repo root; ~2.5 min)
Expected: PASS, with `TestE2E/SecondTenant` covering enroll + un-enroll. If SecondTenant fails, re-run just it against a kept topology: `E2E_KEEP=1 make e2e`, then `cd e2e && go test -tags e2e -run 'TestE2E/SecondTenant' .` — but note the scenario assumes the enrolled state it creates, so full-suite runs are the source of truth.

---

### Task 7: Wrap-up — verify, bins, session notes

**Files:**
- Modify: `.claude/session-history.md`

- [ ] **Step 1: Full verification sweep**

Run: `go build ./... && go vet ./... && go test ./internal/ops/ ./internal/cli/ ./internal/pki/ ./internal/relay/caddy/`
Expected: all clean/PASS. (`make e2e` already passed in Task 6.)

- [ ] **Step 2: Build both binaries and stage tw.exe**

Run:
```bash
make build && make build-windows
cp bin/tw.exe /mnt/c/Users/alial/Downloads/tw.exe
```
Expected: `bin/tw` and `bin/tw.exe` built; staging copy succeeds.

- [ ] **Step 3: Update session history**

Prepend a dated block to `.claude/session-history.md` summarizing: the command, the total-removal semantics (config + live kill), the kill-by-listener-port rationale (shared SSH user), the one-SSH-connection ordering, e2e SecondTenant extension, and that the work is uncommitted (user drives git).

- [ ] **Step 4: Report**

Tell the user: feature done, verified (unit + full e2e outputs), bins staged, NOT committed — awaiting their instruction.
