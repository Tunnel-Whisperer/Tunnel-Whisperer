# Local E2E Compose Environment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A permanently runnable Docker Compose e2e environment (`make e2e`) that exercises every tw role — admin, server, client — over the real relay data path, with a coverage tripwire that fails the ordinary build when a CLI command has no declared e2e coverage.

**Architecture:** Five containers (relay = Debian+systemd provisioned by the real generated install script; admin/server/client/server2 = minimal Debian + fresh `bin/tw`) on one Compose network with a `/shared` handoff volume. The host runs a Go test package (`//go:build e2e`) whose single `TestE2E` drives ordered scenario subtests via `docker compose exec`. TLS works locally via Caddy `local_certs` + OS trust-store injection — zero product-code changes.

**Tech Stack:** Go 1.26 (`GOTOOLCHAIN=auto`), Docker Compose v2, Debian bookworm images, Caddy (apt), Xray (upstream install script), `gopkg.in/yaml.v3` (already a direct dependency).

**Spec:** `docs/superpowers/specs/2026-07-18-local-e2e-compose-design.md` — read it first; it is the authority on scope and scenario semantics.

## Global Constraints

- Go module path is `github.com/tunnelwhisperer/tw`; repo root is the working directory for all commands.
- Do NOT modify product behavior for testability. No `allowInsecure`/CA-pin knobs in tw. Harness shims live only in `e2e/`.
- The suite must run from a clean checkout with only Docker + Go installed: `make e2e`.
- `internal/cli/coverage_test.go` has NO build tag (runs in every `go test ./...`); everything under `e2e/*.go` has `//go:build e2e`.
- Verification floor for every task: `go build ./...` and `go vet ./...` pass.
- Git: the repo owner drives git. Treat every "Commit" step as a checkpoint — stage the listed files and ask the user for approval before committing; never push.
- Known port defaults (from `internal/config/config.go`): server SSH 2222, gRPC API 50051, dashboard 8080, RemotePort 2222 → relay VLESS inbound upstream `h2c://127.0.0.1:12222`.
- Relay domain is `relay.tw.test`, relay static IP `172.28.0.10`, echo target port 7777, user client port 18080, user name `alice`.
- The `tw admin create` wizard's Manual provider is currently option **4** (3 cloud providers + 1). The harness must assert the wizard printed `Select [1-4]` and fail with a clear message if the count changed.
- Every e2e scenario file (`e2e/*_test.go`) begins with exactly:

  ```go
  //go:build e2e

  package e2e

  import (
  	"strings"
  	"testing"
  	"time"
  )
  ```

  (drop `time` or `strings` in files that don't use them; `go vet -tags e2e ./e2e/` will tell you).

---

### Task 1: Coverage tripwire (`internal/cli/coverage_test.go` + `e2e/coverage.yaml`)

**Files:**
- Create: `internal/cli/coverage_test.go`
- Create: `e2e/coverage.yaml`

**Interfaces:**
- Produces: `e2e/coverage.yaml` schema used by all later tasks — top-level `commands:` map from space-joined command path (e.g. `"server user create"`) to EITHER a non-empty list of test names (`[TestE2E/UserLifecycle]`) OR a map `{exempt: "<reason>"}`. Optional top-level `dashboard:` and `api:` maps use the same value schema and are NOT validated by the tripwire (convention-only, per spec).
- Produces: the rule that a *runnable* Cobra command (has `Run`/`RunE`) must appear in `commands:`; group-only commands (`server`, `config`, …) and the auto `help` command are excluded; stale keys (in yaml but not in the tree) are errors.

- [ ] **Step 1: Write the failing test**

`internal/cli/coverage_test.go` (package `cli`, no build tag):

```go
package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// TestEveryCommandHasE2ECoverage walks the real Cobra tree and requires every
// runnable command to be mapped in e2e/coverage.yaml — either to at least one
// e2e test name or to an explicit exemption with a reason. This is the
// mechanical guarantee that the e2e suite cannot silently go stale: adding a
// CLI surface without declaring its coverage fails the ordinary build.
func TestEveryCommandHasE2ECoverage(t *testing.T) {
	data, err := os.ReadFile("../../e2e/coverage.yaml")
	if err != nil {
		t.Fatalf("reading e2e/coverage.yaml: %v (every runnable command needs an entry there)", err)
	}
	var doc struct {
		Commands map[string]yaml.Node `yaml:"commands"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing e2e/coverage.yaml: %v", err)
	}
	if doc.Commands == nil {
		t.Fatal("e2e/coverage.yaml: missing top-level 'commands' map")
	}

	for path, node := range doc.Commands {
		if err := validateEntry(node); err != nil {
			t.Errorf("coverage.yaml entry %q: %v", path, err)
		}
	}

	var leaves []string
	var walk func(c *cobra.Command, prefix []string)
	walk = func(c *cobra.Command, prefix []string) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Hidden {
				continue
			}
			p := append(append([]string{}, prefix...), sub.Name())
			if sub.Runnable() {
				leaves = append(leaves, strings.Join(p, " "))
			}
			walk(sub, p)
		}
	}
	walk(rootCmd, nil)
	sort.Strings(leaves)

	leafSet := map[string]bool{}
	var missing []string
	for _, l := range leaves {
		leafSet[l] = true
		if _, ok := doc.Commands[l]; !ok {
			missing = append(missing, l)
		}
	}
	var stale []string
	for path := range doc.Commands {
		if !leafSet[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("commands with no e2e coverage entry (add them to e2e/coverage.yaml,\n"+
			"mapped to a TestE2E scenario or {exempt: \"reason\"}):\n  %s",
			strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("stale e2e/coverage.yaml entries (command no longer exists — remove or rename):\n  %s",
			strings.Join(stale, "\n  "))
	}
}

// validateEntry accepts a non-empty string list (test names) or a mapping
// containing a non-empty "exempt" reason.
func validateEntry(n yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		var tests []string
		if err := n.Decode(&tests); err != nil {
			return fmt.Errorf("want a list of test names: %w", err)
		}
		if len(tests) == 0 {
			return fmt.Errorf("empty test list")
		}
		for _, s := range tests {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("blank test name")
			}
		}
		return nil
	case yaml.MappingNode:
		var m struct {
			Exempt string `yaml:"exempt"`
		}
		if err := n.Decode(&m); err != nil {
			return fmt.Errorf("want {exempt: \"reason\"}: %w", err)
		}
		if strings.TrimSpace(m.Exempt) == "" {
			return fmt.Errorf("exempt entry needs a non-empty reason")
		}
		return nil
	default:
		return fmt.Errorf("want a list of test names or {exempt: \"reason\"}")
	}
}
```

- [ ] **Step 2: Run it, confirm it fails for the right reason**

Run: `go test ./internal/cli/ -run TestEveryCommandHasE2ECoverage -v`
Expected: FAIL with "reading e2e/coverage.yaml: … no such file".

- [ ] **Step 3: Write the initial `e2e/coverage.yaml`**

Seed it with the mapping below, then let the test's own "missing commands" output correct the list — the tree is the source of truth, not this plan.

```yaml
# Every runnable tw command must map to the e2e scenario(s) that exercise it,
# or carry an explicit {exempt: "reason"}. Enforced by
# internal/cli/coverage_test.go (fails `go test ./...` on drift).
commands:
  "admin create":            [TestE2E/RelayInstall]
  "admin test":              [TestE2E/RelayInstall]
  "admin status":            [TestE2E/RelayInstall, TestE2E/Teardown]
  "admin enroll":            [TestE2E/ServerJoin, TestE2E/SecondTenant]
  "admin servers":           [TestE2E/SecondTenant]
  "admin destroy":           [TestE2E/Teardown]
  "admin ssh":               {exempt: "interactive relay terminal; no scriptable assertion yet"}
  "admin reconcile":         {exempt: "rewrites relay to a single tenant; needs a dedicated scenario before it can run mid-suite"}
  "server join":             [TestE2E/ServerJoin, TestE2E/SecondTenant]
  "server start":            [TestE2E/ServerJoin]
  "server test":             [TestE2E/ServerJoin]
  "server status":           [TestE2E/ServerJoin]
  "server user create":      [TestE2E/UserLifecycle]
  "server user apply":       [TestE2E/UserLifecycle]
  "server user list":        [TestE2E/UserLifecycle]
  "server user delete":      [TestE2E/Revocation]
  "server user unregister":  [TestE2E/Revocation]
  "server user edit":        {exempt: "pending: interactive wizard; cover when mappings-edit gets flags"}
  "server app list":         {exempt: "pending: app catalog scenario not yet written"}
  "server app create":       {exempt: "pending: app catalog scenario not yet written"}
  "server app edit":         {exempt: "pending: app catalog scenario not yet written"}
  "server app delete":       {exempt: "pending: app catalog scenario not yet written"}
  "client connect":          [TestE2E/UserLifecycle]
  "client test":             [TestE2E/UserLifecycle]
  "client status":           [TestE2E/UserLifecycle]
  "client listen":           [TestE2E/UserLifecycle]
  "config import":           [TestE2E/UserLifecycle]
  "config export":           [TestE2E/Contexts]
  "config export-user":      [TestE2E/UserLifecycle]
  "config get-contexts":     [TestE2E/Contexts]
  "config current-context":  [TestE2E/Contexts]
  "config use-context":      [TestE2E/Contexts]
  "config new-context":      [TestE2E/Contexts]
  "config rename-context":   [TestE2E/Contexts]
  "config delete-context":   [TestE2E/Contexts]
  "config view":             [TestE2E/Contexts]
  "service install":         {exempt: "native service-manager integration (systemd/SCM/launchd); exercised via the manual Windows flow in CLAUDE.md"}
  "service uninstall":       {exempt: "native service-manager integration; see 'service install'"}
  "service start":           {exempt: "native service-manager integration; see 'service install'"}
  "service stop":            {exempt: "native service-manager integration; see 'service install'"}
  "proxy":                   {exempt: "pending: upstream-proxy scenario needs a SOCKS5 container"}
  "proxy set":               {exempt: "pending: upstream-proxy scenario needs a SOCKS5 container"}
  "proxy clear":             {exempt: "pending: upstream-proxy scenario needs a SOCKS5 container"}
  "dashboard":               {exempt: "standalone dashboard command; the in-server dashboard is covered by TestE2E/Dashboard"}
  "completion":              {exempt: "shell completion generation; no runtime behavior"}
```

- [ ] **Step 4: Iterate until green**

Run: `go test ./internal/cli/ -run TestEveryCommandHasE2ECoverage -v`
Fix every "missing" / "stale" path the failure output lists (the plan's seed list may not match the tree exactly — trust the test). Expected: PASS.

- [ ] **Step 5: Full-repo check**

Run: `go build ./... && go vet ./... && go test ./internal/cli/ ./internal/pki/ ./internal/relay/caddy/`
Expected: all PASS.

- [ ] **Step 6: Commit checkpoint** — `internal/cli/coverage_test.go`, `e2e/coverage.yaml`; suggested message: `test: enforce e2e coverage mapping for every CLI command`. Ask the user before committing.

---

### Task 2: Images + Compose topology

**Files:**
- Create: `e2e/images/relay/Dockerfile`
- Create: `e2e/images/tw/Dockerfile`
- Create: `e2e/images/tw/echo/main.go`
- Create: `e2e/images/tw/.gitignore` (ignores the staged `tw` and `echo-server` binaries)
- Create: `e2e/docker-compose.yaml`
- Create: `e2e/shared/.gitkeep` and `e2e/.gitignore` (ignore `shared/*` except `.gitkeep`)

**Interfaces:**
- Produces: Compose project name `tw-e2e`; service names `relay`, `admin`, `server`, `client`, `server2`; relay reachable as `relay.tw.test` / `172.28.0.10` from every container; `/shared` bind-mount in all tw containers; `TW_CONFIG_DIR=/etc/tw-test` in tw containers; tw containers idle on `sleep infinity` (processes are started via `docker compose exec`).

- [ ] **Step 1: Relay image**

`e2e/images/relay/Dockerfile` — mirrors the package set of `internal/relay/terraform/install-script.sh.tmpl` so the real script re-runs fast on top:

```dockerfile
# Relay base: Debian + systemd with the install script's packages pre-baked.
# The REAL tw-generated install script provisions this container at test time;
# this image only pre-installs packages so that run is fast. Keep the package
# list in sync with internal/relay/terraform/install-script.sh.tmpl.
FROM debian:bookworm
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y \
      systemd systemd-sysv dbus \
      openssh-server sudo gnupg \
      debian-keyring debian-archive-keyring apt-transport-https curl ufw unzip \
 && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
      | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg \
 && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
      > /etc/apt/sources.list.d/caddy-stable.list \
 && apt-get update && apt-get install -y caddy \
 && bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install \
 && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
```

- [ ] **Step 2: tw image + echo target**

`e2e/images/tw/echo/main.go`:

```go
// Command echo-server is the e2e forward target: a TCP listener that echoes
// every byte back, so the suite can verify tunnel traffic byte-for-byte.
package main

import (
	"flag"
	"io"
	"log"
	"net"
)

func main() {
	port := flag.String("port", "7777", "listen port")
	flag.Parse()
	l, err := net.Listen("tcp", "127.0.0.1:"+*port)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("echo listening on 127.0.0.1:%s", *port)
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go func(c net.Conn) {
			defer c.Close()
			io.Copy(c, c)
		}(c)
	}
}
```

`e2e/images/tw/Dockerfile` (build context receives `tw` and `echo-server` binaries staged by `make e2e`):

```dockerfile
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y \
      ca-certificates curl netcat-openbsd openssl \
 && rm -rf /var/lib/apt/lists/*
COPY tw /usr/local/bin/tw
COPY echo-server /usr/local/bin/echo-server
ENV TW_CONFIG_DIR=/etc/tw-test
WORKDIR /shared
CMD ["sleep", "infinity"]
```

`e2e/images/tw/.gitignore`:

```
tw
echo-server
```

- [ ] **Step 3: Compose file**

`e2e/docker-compose.yaml`:

```yaml
name: tw-e2e

x-tw: &tw
  build: ./images/tw
  volumes:
    - ./shared:/shared
  networks:
    twnet: {}

services:
  relay:
    build: ./images/relay
    privileged: true
    volumes:
      - ./shared:/shared
    networks:
      twnet:
        ipv4_address: 172.28.0.10
        aliases: [relay.tw.test]
  admin:
    <<: *tw
  server:
    <<: *tw
  client:
    <<: *tw
  server2:
    <<: *tw

networks:
  twnet:
    ipam:
      config:
        - subnet: 172.28.0.0/24
```

`e2e/.gitignore`:

```
shared/*
!shared/.gitkeep
```

- [ ] **Step 4: Verify the topology boots**

Stage binaries by hand once (the Makefile target comes in Task 4):

Run:
```bash
GOTOOLCHAIN=auto GOOS=linux GOARCH=amd64 go build -o e2e/images/tw/tw ./cmd/tw
GOTOOLCHAIN=auto GOOS=linux GOARCH=amd64 go build -o e2e/images/tw/echo-server ./e2e/images/tw/echo
docker compose -f e2e/docker-compose.yaml up -d --build
docker compose -f e2e/docker-compose.yaml exec relay systemctl is-system-running --wait || true
docker compose -f e2e/docker-compose.yaml exec admin tw --version
docker compose -f e2e/docker-compose.yaml exec admin getent hosts relay.tw.test
```
Expected: all five services Up; systemd reports `running` (or `degraded` — acceptable, some units can't run in a container); `tw --version` prints a version; `relay.tw.test` resolves to `172.28.0.10`.
If systemd fails to boot at all, try adding `cgroup: host` / `tmpfs: [/run, /run/lock]` to the relay service before reaching for anything else.

- [ ] **Step 5: Tear down**

Run: `docker compose -f e2e/docker-compose.yaml down -v`

- [ ] **Step 6: Commit checkpoint** — `e2e/images/**`, `e2e/docker-compose.yaml`, `e2e/.gitignore`, `e2e/shared/.gitkeep`; suggested message: `test(e2e): compose topology and container images`. Ask the user before committing.

---

### Task 3: Harness + TestE2E skeleton + smoke subtest

**Files:**
- Create: `e2e/harness.go`
- Create: `e2e/e2e_test.go`

**Interfaces:**
- Produces (used by every scenario task):
  - `execIn(t *testing.T, service string, script string) string` — `docker compose exec -T <service> sh -c <script>`, fails the test on error, returns combined output.
  - `execInOK(service, script string) (string, error)` — same, non-fatal.
  - `execDetached(t *testing.T, service, script string)` — `docker compose exec -T -d <service> sh -c <script>` for long-running processes (`tw server start`, `tw client connect`, `echo-server`).
  - `waitFor(t *testing.T, desc string, timeout time.Duration, cond func() (bool, string))` — bounded poll every 2 s; on timeout calls `dumpDiagnostics(t)` then `t.Fatalf` with desc + last status.
  - `dumpDiagnostics(t *testing.T)` — logs `docker compose ps`, `docker compose logs --tail 200`, and relay `journalctl -u caddy -u xray --no-pager -n 100`.
  - Constants: `domain = "relay.tw.test"`, `relayIP = "172.28.0.10"`, `echoPort = "7777"`, `userPort = "18080"`.

- [ ] **Step 1: Write the harness**

`e2e/harness.go`:

```go
//go:build e2e

// Package e2e drives the Docker Compose test topology from the host. All tw
// and relay processes run inside containers; this package only orchestrates
// via `docker compose exec` and asserts on the results. See
// docs/superpowers/specs/2026-07-18-local-e2e-compose-design.md.
package e2e

import (
	"os/exec"
	"testing"
	"time"
)

const (
	domain   = "relay.tw.test"
	relayIP  = "172.28.0.10"
	echoPort = "7777"
	userPort = "18080"
)

func compose(args ...string) *exec.Cmd {
	base := []string{"compose", "-f", "docker-compose.yaml"}
	return exec.Command("docker", append(base, args...)...)
}

// execIn runs a shell script in a service container and fails the test on error.
func execIn(t *testing.T, service, script string) string {
	t.Helper()
	out, err := execInOK(service, script)
	if err != nil {
		dumpDiagnostics(t)
		t.Fatalf("exec in %s failed: %v\nscript: %s\noutput:\n%s", service, err, script, out)
	}
	return out
}

func execInOK(service, script string) (string, error) {
	out, err := compose("exec", "-T", service, "sh", "-c", script).CombinedOutput()
	return string(out), err
}

// execDetached starts a long-running process in a container and returns
// immediately (docker compose exec -d).
func execDetached(t *testing.T, service, script string) {
	t.Helper()
	if out, err := compose("exec", "-T", "-d", service, "sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("detached exec in %s failed: %v\n%s", service, err, out)
	}
}

// waitFor polls cond every 2s until it reports done or the timeout elapses.
func waitFor(t *testing.T, desc string, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		done, status := cond()
		if done {
			return
		}
		last = status
		time.Sleep(2 * time.Second)
	}
	dumpDiagnostics(t)
	t.Fatalf("timed out after %s waiting for %s (last: %s)", timeout, desc, last)
}

func dumpDiagnostics(t *testing.T) {
	t.Helper()
	for _, c := range [][]string{
		{"ps"},
		{"logs", "--tail", "200"},
		{"exec", "-T", "relay", "journalctl", "-u", "caddy", "-u", "xray", "--no-pager", "-n", "100"},
	} {
		out, _ := compose(c...).CombinedOutput()
		t.Logf("--- docker compose %v ---\n%s", c, out)
	}
}

// twServices are the containers that run the tw binary.
var twServices = []string{"admin", "server", "client", "server2"}

// fatalf fails the test after dumping full topology diagnostics.
func fatalf(t *testing.T, format string, args ...any) {
	t.Helper()
	dumpDiagnostics(t)
	t.Fatalf(format, args...)
}
```

- [ ] **Step 2: Write `e2e/e2e_test.go` with the ordered subtest skeleton**

Scenario funcs land in later tasks; reference them all now with placeholders that skip, so the file compiles and the order is fixed from day one:

```go
//go:build e2e

package e2e

import (
	"testing"
)

// TestMain-free: `make e2e` is responsible for compose up/down. TestE2E only
// verifies the topology is up, then runs the scenarios in dependency order.
// Scenarios share container state; use -run 'TestE2E/<Name>' only against an
// already-provisioned topology (E2E_KEEP=1).
func TestE2E(t *testing.T) {
	out, err := compose("ps", "--status", "running", "--services").CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose ps failed — did you run `make e2e`? %v\n%s", err, out)
	}
	for _, svc := range append([]string{"relay"}, twServices...) {
		if !containsLine(string(out), svc) {
			t.Fatalf("service %q is not running — start the topology with `make e2e`\n%s", svc, out)
		}
	}

	steps := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"Smoke", testSmoke},
		{"RelayInstall", testRelayInstall},
		{"ServerJoin", testServerJoin},
		{"MTLSGate", testMTLSGate},
		{"UserLifecycle", testUserLifecycle},
		{"PermitOpen", testPermitOpen},
		{"Revocation", testRevocation},
		{"Contexts", testContexts},
		{"SecondTenant", testSecondTenant},
		{"Dashboard", testDashboard},
		{"RelayResilience", testRelayResilience},
		{"Teardown", testTeardown},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.fn) {
			t.Fatalf("scenario %s failed; later scenarios depend on it — stopping", s.name)
		}
	}
}

func containsLine(haystack, want string) bool {
	for _, l := range splitLines(haystack) {
		if l == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' || r == '\r' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// testSmoke proves the harness itself: tw runs in every tw container and the
// relay resolves.
func testSmoke(t *testing.T) {
	for _, svc := range twServices {
		out := execIn(t, svc, "tw --version")
		t.Logf("%s: %s", svc, out)
	}
	execIn(t, "admin", "getent hosts "+domain)
	execIn(t, "relay", "systemctl is-active ssh || systemctl is-active sshd")
}
```

Placeholder for every not-yet-written scenario func (one per name above), in `e2e/e2e_test.go` for now, moved to their scenario files as those tasks land:

```go
func testRelayInstall(t *testing.T)    { t.Skip("implemented in Task 5") }
// ... same one-liner for each remaining scenario ...
```

- [ ] **Step 3: Compile + run smoke against a live topology**

Run:
```bash
go vet -tags e2e ./e2e/
docker compose -f e2e/docker-compose.yaml up -d --build   # if not still up from Task 2
cd e2e && go test -tags e2e -run 'TestE2E/Smoke' -v . ; cd ..
```
Expected: vet clean; Smoke PASS, all other subtests SKIP.
Note: the test package must be executed with the working directory at `e2e/` (the compose file path is relative). `make e2e` (Task 4) encodes this; keep it in mind for manual runs.

- [ ] **Step 4: Commit checkpoint** — `e2e/harness.go`, `e2e/e2e_test.go`; suggested message: `test(e2e): host-side harness and ordered scenario skeleton`. Ask the user before committing.

---

### Task 4: `make e2e` + CLAUDE.md rule + CI workflow

Do this now, not last: from here on every scenario task verifies via `make e2e`.

**Files:**
- Modify: `Makefile`
- Modify: `CLAUDE.md`
- Create: `.github/workflows/e2e.yml`

**Interfaces:**
- Produces: `make e2e` (full cycle) and `make e2e-up` (leave topology running for `-run` filtering during development).

- [ ] **Step 1: Makefile targets**

Append to `Makefile` (tab-indented recipes; keep `.PHONY` updated):

```make
.PHONY: e2e e2e-up e2e-down

e2e-up:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o e2e/images/tw/tw $(CMD)
	GOOS=linux GOARCH=amd64 go build -o e2e/images/tw/echo-server ./e2e/images/tw/echo
	docker compose -f e2e/docker-compose.yaml up -d --build

e2e-down:
	docker compose -f e2e/docker-compose.yaml down -v

e2e: e2e-up
	cd e2e && go test -tags e2e -timeout 30m -v . ; status=$$?; \
	cd ..; \
	if [ -z "$$E2E_KEEP" ]; then $(MAKE) e2e-down; fi; \
	exit $$status
```

- [ ] **Step 2: Run it**

Run: `E2E_KEEP=1 make e2e`
Expected: images build, topology up, `TestE2E/Smoke` PASS, remaining subtests SKIP, exit 0, topology left running.

- [ ] **Step 3: CLAUDE.md — the living-coverage rule**

Add this section to `CLAUDE.md` after the "Commands" section:

```markdown
## End-to-end suite (must stay current)

`make e2e` runs the full-product e2e suite in Docker Compose (`e2e/`): a real
relay container provisioned by the tw-generated install script, plus
admin/server/client containers, driving every role over the real
VLESS/mTLS/SSH data path. `E2E_KEEP=1 make e2e` leaves the topology up;
`cd e2e && go test -tags e2e -run 'TestE2E/<Scenario>' .` re-runs one scenario
against it. Design doc: `docs/superpowers/specs/2026-07-18-local-e2e-compose-design.md`.

**A feature is not done until:** (a) `make e2e` passes, (b) `e2e/coverage.yaml`
maps every new/changed command to a real scenario (`internal/cli/coverage_test.go`
fails the ordinary build otherwise — never satisfy it with a hollow exemption),
and (c) the scenario proves the feature over the real tunnel where applicable.
The suite must always run from a clean checkout with only Docker + Go.
```

- [ ] **Step 4: CI workflow**

`.github/workflows/e2e.yml`:

```yaml
name: e2e
on:
  push:
    branches: [main]
  pull_request:
jobs:
  e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: make e2e
```

- [ ] **Step 5: Verify**

Run: `go build ./... && go vet ./...`
Expected: PASS. (The workflow itself is proven on the first push — note that in the final report.)

- [ ] **Step 6: Commit checkpoint** — `Makefile`, `CLAUDE.md`, `.github/workflows/e2e.yml`; suggested message: `test(e2e): make e2e entrypoint, CI workflow, coverage rule in CLAUDE.md`. Ask the user before committing.

---

### Task 5: Scenario RelayInstall

**Files:**
- Create: `e2e/relay_install_test.go` (move the placeholder func here)
- Modify: `e2e/e2e_test.go` (remove the placeholder)

**Interfaces:**
- Consumes: harness helpers (Task 3).
- Produces: a provisioned relay trusted by all containers; admin context with relay bundle at `/shared/tw_relay-tw-test.twctx`; Caddy local root CA installed in every tw container (later scenarios depend on all of this).

- [ ] **Step 1: Write the scenario**

`e2e/relay_install_test.go` — the flow, with exact commands:

```go
//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

func testRelayInstall(t *testing.T) {
	// Admin identity: mode must be "admin" before the wizard so the relay
	// handle is rendered with the admin role. Seeding the config file is a
	// harness-only shim (there is no CLI mode command yet).
	execIn(t, "admin", `mkdir -p /etc/tw-test && printf 'mode: admin\n' > /etc/tw-test/config.yaml`)

	// Drive the wizard non-interactively. Prompt order (create_relay.go):
	// domain, provider number (Manual is 4 = 3 cloud providers + 1),
	// relay public IP, "have you run the script? [y/N]".
	// --ssh-open=false suppresses the SSH prompt.
	out := execIn(t, "admin",
		`cd /shared && printf '`+domain+`\n4\n`+relayIP+`\ny\n' | tw admin create --ssh-open=false`)
	if !strings.Contains(out, "Select [1-4]") {
		fatalf(t, "wizard provider list changed — update the scripted stdin (got:\n%s)", out)
	}
	if !strings.Contains(out, "Relay server setup complete") {
		fatalf(t, "wizard did not complete:\n%s", out)
	}

	// The wizard wrote the install script and the admin bundle into /shared.
	execIn(t, "admin", "test -f /shared/tw-install-"+domain+".sh")
	execIn(t, "admin", "test -f /shared/tw_relay-tw-test.twctx")

	// Run the REAL install script on the relay.
	installOut := execIn(t, "relay", "bash /shared/tw-install-"+domain+".sh")
	if !strings.Contains(installOut, "Setup complete") {
		fatalf(t, "install script did not complete:\n%s", installOut)
	}

	// Harness shim 1: force Caddy's internal CA instead of ACME.
	execIn(t, "relay", `grep -q local_certs /etc/caddy/Caddyfile || `+
		`(printf '{\n\tlocal_certs\n}\n' | cat - /etc/caddy/Caddyfile > /tmp/Caddyfile.new `+
		`&& mv /tmp/Caddyfile.new /etc/caddy/Caddyfile && systemctl restart caddy)`)

	// Harness shim 2: trust Caddy's local root everywhere.
	waitFor(t, "caddy local root CA", 60*time.Second, func() (bool, string) {
		out, err := execInOK("relay",
			"cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt")
		if err != nil || !strings.Contains(out, "BEGIN CERTIFICATE") {
			return false, "root.crt not present yet"
		}
		return true, ""
	})
	execIn(t, "relay",
		"cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt /shared/caddy-root.crt")
	for _, svc := range twServices {
		execIn(t, svc,
			"cp /shared/caddy-root.crt /usr/local/share/ca-certificates/tw-e2e-root.crt && update-ca-certificates")
	}

	// Services up.
	for _, unit := range []string{"caddy", "xray"} {
		execIn(t, "relay", "systemctl is-active "+unit)
	}

	// End-to-end admin check: DNS + mTLS handshake + SSH over the VLESS tunnel.
	waitFor(t, "tw admin test", 120*time.Second, func() (bool, string) {
		out, err := execInOK("admin", "tw admin test")
		if err != nil {
			return false, out
		}
		if strings.Contains(out, "✗") {
			return false, out
		}
		return strings.Contains(out, "tunnel and shell working"), out
	})

	// Status shows the manual relay.
	statusOut := execIn(t, "admin", "tw admin status")
	if !strings.Contains(statusOut, domain) {
		fatalf(t, "admin status does not mention the relay:\n%s", statusOut)
	}

	// Idempotency: the script's documented contract is that a re-run cleans
	// prior state. Re-run, re-apply the local_certs shim, re-verify.
	execIn(t, "relay", "bash /shared/tw-install-"+domain+".sh")
	execIn(t, "relay", `grep -q local_certs /etc/caddy/Caddyfile || `+
		`(printf '{\n\tlocal_certs\n}\n' | cat - /etc/caddy/Caddyfile > /tmp/Caddyfile.new `+
		`&& mv /tmp/Caddyfile.new /etc/caddy/Caddyfile && systemctl restart caddy)`)
	waitFor(t, "tw admin test after re-install", 120*time.Second, func() (bool, string) {
		out, err := execInOK("admin", "tw admin test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})
}
```

- [ ] **Step 2: Run it**

Run: `cd e2e && go test -tags e2e -run 'TestE2E/(Smoke|RelayInstall)' -v . ; cd ..`
Expected: PASS. Two likely first-run failures and their intended fixes:
  - `ufw` failing inside the container aborts the script (`set -e`). If so, add to the **relay Dockerfile** (not the script): `RUN dpkg-divert --local --rename /usr/sbin/ufw && printf '#!/bin/sh\nexit 0\n' > /usr/sbin/ufw && chmod +x /usr/sbin/ufw` with a comment that the firewall layer is out of e2e scope — and `log`/`t.Log` it in the scenario so the shim is visible.
  - Seeding `config.yaml` with only `mode: admin` might clash with how `config.Load` merges defaults. Verify with `tw config view` on the admin container; if defaults are missing, the correct fix is discussed with the user (options: harness writes a fuller seed produced by `tw config view` from a scratch run, or a tiny `tw admin init` product command).

- [ ] **Step 3: Commit checkpoint** — suggested message: `test(e2e): relay install scenario over the real install script`. Ask the user before committing.

---

### Task 6: Scenarios ServerJoin + MTLSGate

**Files:**
- Create: `e2e/server_test.go` (testServerJoin, testMTLSGate; remove placeholders)

**Interfaces:**
- Consumes: provisioned relay (Task 5).
- Produces: running `tw server start` on `server` with the echo target on `127.0.0.1:7777`; join-response flow proven. Later scenarios assume the server daemon stays up.

- [ ] **Step 1: Write testServerJoin**

```go
func testServerJoin(t *testing.T) {
	// 1. Server generates identity + join request (this also sets mode=server).
	execIn(t, "server", "cd /shared && rm -f tw_join_*.json && tw server join "+domain)
	// 2. Admin enrolls it (SSH to relay over the VLESS tunnel) and writes the response.
	execIn(t, "admin", "cd /shared && rm -f tw_join_response_*.json && tw admin enroll /shared/tw_join_*.json")
	// 3. Server applies the response.
	execIn(t, "server", "cd /shared && tw server join --apply /shared/tw_join_response_*.json")

	// 4. Echo target + server daemon.
	execDetached(t, "server", "echo-server -port "+echoPort)
	execDetached(t, "server", "tw server start > /var/log/tw-server.log 2>&1")

	waitFor(t, "server tunnel up", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})
	out := execIn(t, "server", "tw server status")
	t.Logf("server status:\n%s", out)
}
```

Note for the implementer: the shell glob `tw_join_*.json` relies on exactly one request/response pair in `/shared`; the `rm -f` lines keep that invariant. If `tw admin enroll` cannot expand the glob (single-arg command), wrap with `sh -c 'tw admin enroll /shared/tw_join_*.json'` — `execIn` already runs under `sh -c`.

- [ ] **Step 2: Write testMTLSGate**

```go
func testMTLSGate(t *testing.T) {
	// No client cert: TLS handshake must be rejected by the client_auth gate.
	out, err := execInOK("client", "curl -sS --max-time 10 https://"+domain+"/ 2>&1")
	if err == nil {
		fatalf(t, "HTTPS without a client cert unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "certificate") && !strings.Contains(out, "handshake") {
		fatalf(t, "expected a TLS certificate error, got:\n%s", out)
	}

	// Foreign CA: a self-signed cert must be rejected too.
	execIn(t, "client", `cd /tmp && openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 `+
		`-keyout fake.key -out fake.crt -days 1 -nodes -subj /CN=intruder 2>/dev/null`)
	out, err = execInOK("client",
		"curl -sS --max-time 10 --cert /tmp/fake.crt --key /tmp/fake.key https://"+domain+"/ 2>&1")
	if err == nil {
		fatalf(t, "HTTPS with a foreign-CA cert unexpectedly succeeded:\n%s", out)
	}
}
```

- [ ] **Step 3: Run**

Run: `cd e2e && go test -tags e2e -run 'TestE2E' -v . ; cd ..` (full chain to here)
Expected: Smoke, RelayInstall, ServerJoin, MTLSGate PASS; rest SKIP.

- [ ] **Step 4: Commit checkpoint** — suggested message: `test(e2e): server join/enroll and mTLS gate scenarios`. Ask the user before committing.

---

### Task 7: Scenarios UserLifecycle + PermitOpen + Revocation

**Files:**
- Create: `e2e/users_test.go` (testUserLifecycle, testPermitOpen, testRevocation; remove placeholders)

**Interfaces:**
- Consumes: running server daemon (Task 6).
- Produces: user `alice` created, exported, imported on `client`, live `tw client connect`; then revoked (client left disconnected — Contexts and later scenarios must not assume alice works after Revocation).

- [ ] **Step 1: testUserLifecycle**

```go
func testUserLifecycle(t *testing.T) {
	// Create + register + list (server daemon is running; CLI goes via gRPC where wired).
	execIn(t, "server", "tw server user create alice -m "+userPort+":"+echoPort)
	execIn(t, "server", "tw server user apply alice")
	out := execIn(t, "server", "tw server user list")
	if !strings.Contains(out, "alice") {
		fatalf(t, "alice missing from user list:\n%s", out)
	}

	// Export as a client context bundle; import + activate on the client.
	execIn(t, "server", "cd /shared && rm -f alice-tw-context.twctx && tw config export-user alice")
	execIn(t, "client", "tw config import /shared/alice-tw-context.twctx --activate")
	execIn(t, "client", "tw client listen") // prints current listen address; covers the command

	// Connect and prove byte-for-byte traffic through relay + tunnel.
	execDetached(t, "client", "tw client connect > /var/log/tw-client.log 2>&1")
	waitFor(t, "client tunnel listening", 120*time.Second, func() (bool, string) {
		_, err := execInOK("client", "nc -z 127.0.0.1 "+userPort)
		if err != nil {
			out, _ := execInOK("client", "tail -5 /var/log/tw-client.log")
			return false, out
		}
		return true, ""
	})
	echoOut := execIn(t, "client",
		"printf 'hello-tw-e2e' | nc -w 10 127.0.0.1 "+userPort)
	if strings.TrimSpace(echoOut) != "hello-tw-e2e" {
		fatalf(t, "echo round-trip mismatch: %q", echoOut)
	}

	execIn(t, "client", "tw client status")
	waitFor(t, "tw client test", 60*time.Second, func() (bool, string) {
		out, err := execInOK("client", "tw client test")
		return err == nil && !strings.Contains(out, "✗"), out
	})
}
```

- [ ] **Step 2: testPermitOpen**

```go
func testPermitOpen(t *testing.T) {
	// The authorized_keys entry for alice must carry ONLY the granted
	// permitopen target plus single-session — that's the server-side gate
	// (re-read on every auth attempt).
	ak := execIn(t, "server", "cat /etc/tw-test/authorized_keys")
	line := ""
	for _, l := range splitLines(ak) {
		if strings.Contains(l, "alice@tw") {
			line = l
			break
		}
	}
	if line == "" {
		fatalf(t, "no authorized_keys entry for alice:\n%s", ak)
	}
	if !strings.Contains(line, `permitopen="127.0.0.1:`+echoPort+`"`) {
		fatalf(t, "alice entry missing permitopen for the granted port: %s", line)
	}
	if strings.Contains(line, `permitopen="127.0.0.1:2222"`) {
		fatalf(t, "alice entry grants the server SSH port — must not: %s", line)
	}
	if !strings.Contains(line, "single-session") {
		fatalf(t, "alice entry missing single-session: %s", line)
	}

	// single-session at runtime: a second concurrent connect for the same
	// user must fail while the first is up.
	out, err := execInOK("client", "TW_CONFIG_DIR=/etc/tw-test timeout 20 tw client connect 2>&1")
	if err == nil && !strings.Contains(out, "single-session") && !strings.Contains(out, "rejected") {
		fatalf(t, "second concurrent session unexpectedly connected:\n%s", out)
	}
}
```

Note: the authorized_keys path is confirmed as `<TW_CONFIG_DIR>/authorized_keys` (`config.AuthorizedKeysPath()`), i.e. `/etc/tw-test/authorized_keys` in the container. If the second-session probe turns out to succeed because the daemon multiplexes (not a second auth), replace it with a file-level assertion only and record why in a comment.

- [ ] **Step 3: testRevocation**

```go
func testRevocation(t *testing.T) {
	// Unregister from the relay, then delete. Both prompt [y/N].
	execIn(t, "server", "printf 'y\\n' | tw server user unregister alice")
	execIn(t, "server", "printf 'y\\n' | tw server user delete alice")

	// Kill the client's live connection, then prove a fresh connect fails
	// WITHOUT any server restart (authorized_keys re-read per auth attempt).
	execIn(t, "client", "pkill -f 'tw client connect' || true")
	execDetached(t, "client", "tw client connect > /var/log/tw-client-revoked.log 2>&1")
	waitFor(t, "revoked client stays down", 60*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+userPort); err == nil {
			return false, "tunnel port still answering"
		}
		out, _ := execInOK("client", "tail -5 /var/log/tw-client-revoked.log")
		return true, out
	})
	execIn(t, "client", "pkill -f 'tw client connect' || true")
}
```

Caution for the implementer: `waitFor` here returns success on the FIRST failed probe — that's too weak (the tunnel may just be slow to come up). Strengthen it: poll for 30 s and require the port to NEVER answer during that window; fail fast if it ever answers. Write that loop explicitly rather than using waitFor.

- [ ] **Step 4: Run**

Run: `cd e2e && go test -tags e2e -run 'TestE2E' -v . ; cd ..`
Expected: everything through Revocation PASS; rest SKIP.

- [ ] **Step 5: Commit checkpoint** — suggested message: `test(e2e): user lifecycle, permitopen/single-session, live revocation`. Ask the user before committing.

---

### Task 8: Scenarios Contexts + SecondTenant

**Files:**
- Create: `e2e/client_test.go` (testContexts), `e2e/admin_test.go` (testSecondTenant; remove placeholders)

**Interfaces:**
- Consumes: client has the imported alice context (name = relay domain by default — discover it via `tw config current-context`).
- Produces: `server2` enrolled and running (left up for RelayResilience).

- [ ] **Step 1: testContexts** (all on the `client` container)

```go
func testContexts(t *testing.T) {
	cur := strings.TrimSpace(execIn(t, "client", "tw config current-context"))
	if cur == "" {
		fatalf(t, "no current context on client")
	}

	execIn(t, "client", "tw config new-context scratch")
	if got := strings.TrimSpace(execIn(t, "client", "tw config current-context")); got != "scratch" {
		fatalf(t, "expected current context scratch, got %q", got)
	}
	out := execIn(t, "client", "tw config get-contexts")
	if !strings.Contains(out, "scratch") || !strings.Contains(out, cur) {
		fatalf(t, "get-contexts missing entries:\n%s", out)
	}

	execIn(t, "client", "tw config rename-context scratch scratch2")
	execIn(t, "client", "tw config use-context "+cur)
	execIn(t, "client", "tw config view")
	execIn(t, "client", "cd /shared && tw config export "+cur)
	execIn(t, "client", "tw config delete-context scratch2")
	out = execIn(t, "client", "tw config get-contexts")
	if strings.Contains(out, "scratch2") {
		fatalf(t, "scratch2 still listed after delete:\n%s", out)
	}
}
```

- [ ] **Step 2: testSecondTenant** (mirrors ServerJoin on `server2`, then multi-tenant asserts)

```go
func testSecondTenant(t *testing.T) {
	execIn(t, "server2", "cd /shared && rm -f tw_join_*.json && tw server join "+domain)
	execIn(t, "admin", "cd /shared && rm -f tw_join_response_*.json && tw admin enroll /shared/tw_join_*.json")
	execIn(t, "server2", "cd /shared && tw server join --apply /shared/tw_join_response_*.json")
	execDetached(t, "server2", "tw server start > /var/log/tw-server.log 2>&1")

	waitFor(t, "server2 tunnel up", 120*time.Second, func() (bool, string) {
		out, err := execInOK("server2", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})

	out := execIn(t, "admin", "tw admin servers")
	if strings.Count(out, "\n") < 3 { // header + >=2 rows
		fatalf(t, "expected two enrolled servers:\n%s", out)
	}

	// First tenant must still work after the relay was re-rendered.
	waitFor(t, "server1 still up", 60*time.Second, func() (bool, string) {
		out, err := execInOK("server", "tw server test")
		return err == nil && strings.Contains(out, "tunnel and shell working"), out
	})
}
```

- [ ] **Step 3: Run + commit checkpoint** — run the full chain as before; suggested message: `test(e2e): context management and multi-tenant enroll scenarios`. Ask the user before committing.

---

### Task 9: Scenarios Dashboard + RelayResilience + Teardown

**Files:**
- Create: `e2e/dashboard_test.go` (testDashboard); extend `e2e/admin_test.go` (testRelayResilience, testTeardown); remove the last placeholders from `e2e/e2e_test.go`.

- [ ] **Step 1: testDashboard** (server daemon serves it on :8080 inside the `server` container)

SSE is session-scoped: a dashboard API operation returns `{"session_id": "..."}` and its progress streams from `/api/events/<session_id>` (`handlers_sse.go`, `server.go:142`). Use the non-destructive relay test as the SSE-producing operation:

```go
func testDashboard(t *testing.T) {
	out := execIn(t, "server", "curl -sS -o /dev/null -w '%{http_code}' http://localhost:8080/")
	if strings.TrimSpace(out) != "200" {
		fatalf(t, "dashboard returned %s", out)
	}
	execIn(t, "server", "curl -sS http://localhost:8080/api/status | grep -q '\"'")

	// Kick off a relay test via the API, then stream its SSE session.
	sid := execIn(t, "server",
		`curl -sS -X POST http://localhost:8080/api/relay/test | sed -n 's/.*"session_id":"\([^"]*\)".*/\1/p'`)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		fatalf(t, "no session_id from /api/relay/test")
	}
	sse := execIn(t, "server",
		"curl -sS --max-time 30 -N http://localhost:8080/api/events/"+sid+" || true")
	if !strings.Contains(sse, "data:") {
		fatalf(t, "no SSE event received:\n%s", sse)
	}
}
```

- [ ] **Step 2: testRelayResilience**

```go
func testRelayResilience(t *testing.T) {
	if out, err := compose("restart", "relay").CombinedOutput(); err != nil {
		t.Fatalf("restarting relay: %v\n%s", err, out)
	}
	// systemd reboots caddy+xray inside; both server tunnels must recover.
	for _, svc := range []string{"server", "server2"} {
		waitFor(t, svc+" recovers after relay restart", 180*time.Second, func() (bool, string) {
			out, err := execInOK(svc, "tw server test")
			return err == nil && strings.Contains(out, "tunnel and shell working"), out
		})
	}
}
```

- [ ] **Step 3: testTeardown**

```go
func testTeardown(t *testing.T) {
	// destroy_relay.go prompts "Destroy this relay? [y/N]" (no AWS creds for a
	// manual relay) and prints "Relay destroyed." on success.
	out := execIn(t, "admin", "printf 'y\\n' | tw admin destroy")
	if !strings.Contains(out, "Relay destroyed.") {
		fatalf(t, "destroy did not complete:\n%s", out)
	}
	// status.go prints "Provisioned: false" once the marker is gone.
	statusOut := execIn(t, "admin", "tw admin status")
	if !strings.Contains(statusOut, "Provisioned: false") {
		fatalf(t, "relay still reported after destroy:\n%s", statusOut)
	}
}
```

- [ ] **Step 4: Full suite, twice**

Run: `make e2e` (fresh topology, no E2E_KEEP), then `make e2e` again.
Expected: all 12 subtests PASS both times — the second run proves the suite is self-contained and repeatable from scratch.

- [ ] **Step 5: Sync coverage.yaml**

Run: `go test ./internal/cli/ -run TestEveryCommandHasE2ECoverage -v`
Every scenario name referenced in `e2e/coverage.yaml` must now exist; update any mapping that drifted during implementation (e.g. a command that ended up covered by a different scenario). Expected: PASS, and a manual skim confirms no mapping lies.

- [ ] **Step 6: Final verification + commit checkpoint**

Run: `go build ./... && go vet ./... && go vet -tags e2e ./e2e/ && go test ./internal/cli/ ./internal/pki/ ./internal/relay/caddy/`
Expected: all PASS. Suggested message: `test(e2e): dashboard, relay resilience, and teardown scenarios`. Ask the user before committing. Then report: suite runtime, any shims applied (ufw stub? config seed?), and remind that the CI workflow proves itself on first push.
