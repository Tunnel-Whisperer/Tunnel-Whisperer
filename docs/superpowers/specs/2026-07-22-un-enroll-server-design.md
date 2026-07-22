# `tw relay un-enroll-server` — remove an enrolled server from the relay

Date: 2026-07-22
Status: approved

## Problem

Enrollment is one-way: `tw relay enroll-server` adds a tenant, but nothing
removes one. The admin needs to decommission a server — and removal must be
total: config gone *and* every live connection for that tenant severed
(sshd sessions survive an authorized_keys rewrite, as the second-tenant
corruption bug demonstrated).

## Command

`tw relay un-enroll-server <server-id>` (admin mode only, `requireMode("admin")`).

- `<server-id>` must match a registry entry exactly; otherwise error
  `server %q is not enrolled`.
- Prints the server's details (port, enrolled date) and prompts
  `really un-enroll? [y/N]`. `--yes` skips the prompt (scripts/e2e).
- Relay unreachable → fail hard (same policy as `get-servers`); the registry
  entry is NOT deleted, so re-running retries cleanly.
- Closing message notes the un-enrolled server keeps its local config and
  must run `tw server join` again to re-enroll.

## Removal flow (`ops.UnenrollServer(serverID string, progress ProgressFunc)`)

Mirrors `EnrollServer`, in `internal/ops/enroll.go`. Tenant list = admin's
own entry + all registered servers *minus* the target. All file writes are
full rewrites from that list (established philosophy: idempotent,
self-healing).

Order matters — block re-entry first, then drop live state, then clean files,
then forget locally:

1. **Resolve** the target from the registry; build the filtered tenant list.
2. Over `RelaySSH`:
   a. **Rewrite authorized_keys** in full (target's line gone → its next SSH
      auth fails). `renderRelayAuthorizedKeys` unchanged.
   b. **Live-remove via gRPC** (same channel as `liveAddTenant`):
      `RemoveRule allow-<id>`, `RemoveRule deny-<id>`, then
      `RemoveInbound vless-in-<id>` (rules reference the inbound, so rules
      first). Removing the inbound severs the tenant's established VLESS
      sessions — the server's transport and all of its clients.
      "Tag not found" errors are tolerated and logged (already gone after an
      earlier partial run), other gRPC errors fail the command.
   c. **Kill the tenant's sshd session**: the established reverse-tunnel
      session survives (a) and holds the listener on
      `127.0.0.1:<RemotePort>`. All tenants (and the admin's own management
      session) share one SSH user, so kill by port, not by name: shell
      one-liner reads `/proc/net/tcp{,6}`, finds the LISTEN socket inode for
      the hex port, walks `/proc/<pid>/fd` for `socket:[inode]`, and
      `sudo kill`s the owning pid. Pure coreutils — no `ss`/`fuser`
      dependency (minimal e2e relay image, arbitrary real relays).
      No listener found = success (tunnel already down).
   d. **Rewrite the Caddyfile** from the filtered list → `caddy validate` →
      graceful `systemctl reload caddy`. Delete `/etc/caddy/ca/<id>.crt`.
   e. **Write the full xray config.json** (persistence across restarts).
      No xray restart — (b) already updated the running process.
3. **Delete `<config>/servers/<id>.json`** — only after all relay work
   succeeded. A mid-way failure leaves the registry intact; re-running the
   command is safe because every step is idempotent.

Progress via `ProgressFunc` events like enroll (CLI prints via `cliProgress`).

## Non-goals

- No cleanup on the un-enrolled server itself (admin can't reach it).
- No soft-disable / re-enable state; re-enrolling is a fresh join.
- No admin-side user cleanup: tenant users live on the tenant's own server.

## Testing

- Unit: tenant-list filtering (target excluded, admin + others kept);
  kill-by-port shell command construction (hex port formatting).
- e2e (`SecondTenant` extension): with server2 running and its tunnel live,
  `tw relay un-enroll-server <id> --yes`; assert
  - the relay holds no listener on server2's port
    (`docker exec` reads `/proc/net/tcp{,6}` on the relay) — proves the kill,
  - `get-servers` no longer lists server2,
  - server-1 `tw server test` and admin `tw relay test` still pass
    (non-disruption),
  - server2 `tw server test` now fails.
- `e2e/coverage.yaml` entry: `relay un-enroll-server` → SecondTenant.

## Rejected alternatives

- **Config rewrite + xray restart**: restart kills every tenant's tunnel and
  the admin's own SSH session mid-command; live gRPC removal is proven by
  enroll.
- **Soft-disable registry flag**: YAGNI.
- **`pkill sshd`-style cleanup**: indistinguishable sessions (shared SSH
  user) would kill the admin's own connection; kill-by-listener-port is
  precise.
