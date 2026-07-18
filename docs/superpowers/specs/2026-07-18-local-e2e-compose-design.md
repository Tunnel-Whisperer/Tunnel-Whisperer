# Local end-to-end test environment (Docker Compose)

Date: 2026-07-18
Status: approved design, pending implementation plan

## Goal

A permanently runnable, always-current end-to-end environment that exercises
every user-facing tw capability — across all three roles (admin, server,
client/user) — against a real relay, over the real data path
(Xray VLESS/XHTTP/mTLS → Caddy → reverse SSH), on one local machine.

This complements unit tests: unit tests verify functions; this suite verifies
the product *in action*. Every feature added to tw must be observable working
here, forever. Staleness is prevented mechanically (see Coverage contract),
not by discipline.

Non-goals: Terraform cloud provisioning (covered by render tests), real ACME
against Let's Encrypt, Windows service integration (existing manual host flow),
Kubernetes (Compose now; the images/scripts stay k8s-friendly if a need
appears).

## Topology

Docker Compose project under `e2e/`, one bridge network.

| Service  | Image | Role |
|----------|-------|------|
| `relay`  | `e2e/images/relay/` — Debian + systemd, with Caddy, Xray, openssh-server, ufw, unzip **pre-installed** (mirrors the install script's package list) | Boots `/sbin/init`; the harness runs the *real* generated install script inside it on suite start. Network alias `relay.tw.test`, static IP `172.28.0.10`. `privileged: true` (systemd requirement). |
| `admin`  | `e2e/images/tw/` — minimal Debian + freshly built linux `bin/tw`, `TW_CONFIG_DIR=/etc/tw-test` | The relay owner. Runs the `tw admin create` wizard (Manual provider), enrolls servers (`tw admin enroll`), holds the admin identity whose CA the relay trusts from install time. |
| `server` | same `e2e/images/tw/` image | Joins via `tw server join` → admin enroll → `--apply`, then runs `tw server start`; also hosts the forward target (`e2e/images/tw/echo` — tiny TCP echo listener) to prove end-to-end traffic. |
| `client` | same `e2e/images/tw/` image | Imports a user context bundle, runs `tw client connect`. |
| `server2`| same image | Second tenant for the multi-tenant enroll scenario. |

All tw containers share a `./shared:/shared` volume for file handoffs (install
script, join request/response JSON, `.twctx` bundles).

The test runner runs on the host as a Go test package (`e2e/e2e_test.go` +
scenario files, build tag `//go:build e2e`) and drives containers via
`docker compose exec`. One top-level `TestE2E` runs the scenarios as ordered
`t.Run` subtests (they share topology state), so `coverage.yaml` references
`TestE2E/<Scenario>` names and `-run 'TestE2E/<Scenario>'` filters work.

Entry point: `make e2e` = build linux tw → `docker compose up -d --build` →
`go test -tags e2e ./e2e/ -v` → `docker compose down -v` (kept up with
`E2E_KEEP=1` for debugging). Must work from a clean checkout with only
Docker + Go installed.

## Fidelity principle (Approach C)

The relay is provisioned by the same artifact a user runs on a real VM: the
harness executes `tw admin create --ssh-open=false` on the admin container,
selects the Manual provider by feeding scripted stdin to the wizard, and runs
the emitted `tw-install-<domain>.sh` inside the relay container. The pre-baked base image only pre-installs
packages for speed; the script's documented idempotent-cleanup behavior makes
"re-run over an existing install" a supported, and now continuously tested,
path. The install script, Caddyfile, Xray relay config, sshd drop-in, and
firewall setup are all product-rendered — the suite must never re-implement
them.

If wizard-driving via stdin proves brittle, the fix is adding non-interactive
flags to the wizards (a product improvement), never scraping around them.

## TLS and DNS (harness shims only — zero product code)

tw clients validate the relay certificate against the system trust store
(there is deliberately no `allowInsecure`/CA-pin knob; do not add one).
Locally:

- After install, the harness prepends a global `{ local_certs }` block to
  `/etc/caddy/Caddyfile` and reloads Caddy, so it issues from its internal CA
  instead of ACME. The product-rendered site block is untouched.
- Caddy's local root
  (`/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt`) is
  copied into `server`/`client`/`server2` at
  `/usr/local/share/ca-certificates/` + `update-ca-certificates`.
- `relay.tw.test` resolves via the Compose network alias; xray SNI follows
  `RelayHost`, so certificate names line up automatically.

## Coverage contract (the "always up to date" mechanism)

`e2e/coverage.yaml` maps **every Cobra command path** to either the e2e test
name(s) that exercise it, or an explicit exemption:

```yaml
"server user create": [TestUserLifecycle]
"admin enroll":       [TestAdminEnroll]
"completion":         {exempt: "shell completion, no runtime behavior"}
```

A plain unit test, `internal/cli/coverage_test.go` (no build tag — runs in
every `go test ./...`), walks the root command tree and fails with the exact
missing paths if any command is absent from the map. Renamed/removed commands
fail it too (stale keys are errors). Net effect: adding a CLI surface without
declaring its e2e coverage breaks the ordinary build, in CI and locally,
before the feature can merge.

Non-CLI surfaces (dashboard pages/SSE/WS, gRPC API methods) are listed in the
same file under `dashboard:` / `api:` prefixes; those sections are maintained
by convention and checked in review, since there's no single tree to walk.

CLAUDE.md gets a section stating the rule: **a feature is not done until
(a) `make e2e` passes, (b) `coverage.yaml` maps its commands to a real
scenario, and (c) the scenario proves the feature over the real tunnel where
applicable.** Exemptions require a reason string and should be rare.

## Scenario suite (initial)

Ordered by dependency; later subtests reuse earlier state within one compose
session (`TestE2E/<name>`). Note the relay initially trusts only the admin's
CA, so even the first server goes through join → enroll.

1. **RelayInstall** — `tw admin create` wizard (Manual provider, scripted
   stdin) generates the script; script runs clean on the base image;
   Caddy/Xray/sshd active; `local_certs` + trust-store shims applied;
   `tw admin test` passes (DNS, mTLS handshake, SSH-over-tunnel);
   re-running the script is idempotent.
2. **ServerJoin** — first server: `tw server join relay.tw.test` →
   `tw admin enroll` → `tw server join --apply` → `tw server start`;
   reverse tunnel published; `tw server status` healthy; `tw server test`
   passes.
3. **MTLSGate** — TLS handshake without a client cert is rejected; a cert
   from a foreign CA is rejected (the admitted-cert case is proven by
   RelayInstall/ServerJoin).
4. **UserLifecycle** — `tw server user create` + `apply` + `list` → `tw
   config export-user` → `tw config import --activate` on `client` → `tw
   client connect` → TCP round-trip through the tunnel to the echo target
   (payload verified byte-for-byte); `tw client test`/`status` pass.
5. **PermitOpen** — the user's `authorized_keys` entry carries only the
   granted `permitopen` targets plus `single-session`; a second concurrent
   session for the same user is refused.
6. **Revocation** — delete user → new connection rejected without any server
   restart (authorized_keys re-read per auth attempt).
7. **Contexts** — `new-context`, `use-context` switch and back,
   `get-contexts`, `current-context`, `rename-context`, `delete-context`,
   `view`, `export` on the client.
8. **SecondTenant** — `server2` joins and is enrolled alongside the first;
   `tw admin servers` lists both; server1's traffic still flows.
9. **Dashboard** — HTTP 200 on the server dashboard (:8080), SSE stream
   delivers events during a reconnect, log console shows entries.
10. **RelayResilience** — restart the relay container mid-session; server
    reverse tunnel and client connection recover; traffic flows again.
11. **Teardown** — `tw admin destroy` removes the manual relay marker and
    deactivates users; `tw admin status` reports no relay.

New features append scenarios here (enforced by the coverage tripwire); this
list is the floor, not the ceiling.

## Error handling & flakiness policy

- Every wait is a bounded poll (helper `waitFor(t, timeout, cond)`), never a
  bare sleep; timeouts generous (relay install dominates, minutes not
  seconds).
- On failure, the harness dumps `docker compose logs` plus `journalctl -u
  caddy -u xray` from the relay into the test output before teardown.
- The suite is serial by design (shared topology); no `t.Parallel()`.

## CI

`.github/workflows/e2e.yml`: run `make e2e` on pull requests and pushes to
`main`. GitHub's ubuntu runners support privileged containers and systemd in
Docker. Base images are built in-workflow with layer caching; if runtime
becomes a problem, publish the relay base image to GHCR later — not in the
initial scope.

## Directory layout

```
e2e/
  docker-compose.yaml
  coverage.yaml
  shared/                     # bind-mounted /shared handoff volume (gitignored contents)
  images/
    relay/Dockerfile          # Debian + systemd + pre-installed packages
    tw/Dockerfile             # minimal Debian + bin/tw + echo-server
    tw/echo/main.go           # tiny TCP echo target, built by make e2e
  harness.go                  # compose exec helpers, waitFor, log dump (build tag e2e)
  e2e_test.go                 # TestMain + TestE2E orchestrating ordered subtests
  relay_install_test.go server_test.go users_test.go client_test.go
  admin_test.go dashboard_test.go    # scenario funcs, //go:build e2e
internal/cli/coverage_test.go # coverage tripwire (no build tag)
```

## Risks

- **systemd-in-Docker** needs `privileged: true`; acceptable for a test
  harness, works on WSL2 and GitHub runners.
- **Wizard stdin scripting** is coupled to prompt wording; mitigation above
  (promote to non-interactive flags when it first breaks).
- **Suite duration** — full run estimated 3–6 min after image cache warm-up;
  `-run` filtering keeps the inner loop short.
