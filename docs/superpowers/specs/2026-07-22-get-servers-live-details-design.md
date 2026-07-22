# `tw relay get-servers` live details — design

Date: 2026-07-22
Status: approved

## Problem

`get-servers` printed only SERVER-ID/HOSTNAME/REMOTE-PORT from the local
registry — no path, no enrollment time, and no way to tell whether a
server's tunnel is actually up.

## Decisions (user-confirmed)

- The command queries the relay live. If the relay is unreachable, it
  **fails hard** with the error — no stale table (scripting-safe).
- Columns: SERVER-ID, PATH, PORT, ENROLLED, TUNNEL.

## Design

- `RegisteredServer` gains `enrolled_at` (UTC RFC3339), stamped in
  `AddServer`. Entries from before the field show `-`.
- New `ops.GetServerDetails() ([]ServerDetail, error)`: reads the registry;
  empty → returns empty without dialing. Otherwise opens ONE SSH session to
  the relay (RelaySSH over the VLESS tunnel) and runs `ss -tln` once. A
  server's TUNNEL is `up` iff the relay listens on `127.0.0.1:<RemotePort>`
  — that listener IS its established reverse forward. `parseListeningPorts`
  is a pure helper over the `ss` output.
- PATH is derived: `/tw/<server-id>`.
- CLI table; ENROLLED formatted `2006-01-02T15:04` (no space — rows stay
  regex/awk-friendly).

## Testing

- Unit: `parseListeningPorts` (ss output variants); `AddServer` stamps
  `EnrolledAt`.
- e2e SecondTenant: get-servers now asserts both `/tw/` paths listed,
  server-1's row TUNNEL `up` (daemon running since ServerJoin), server-2's
  `down` (not started at that point).

## Out of scope

- Config-drift checks (Caddyfile/xray presence per tenant).
- A `--local` offline mode.
