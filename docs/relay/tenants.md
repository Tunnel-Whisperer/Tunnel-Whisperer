# Tenant Management

One relay can carry many servers. Each server is a **tenant**: it gets its own
path on the relay (`/tw/<server-id>`), its own VLESS UUID and inbound, its own
CA in the mTLS trust pool, and its own reverse-tunnel port. The relay admin
admits and removes tenants at will — live, without restarting the relay's Xray
and without disturbing the other tenants.

The admin's source of truth is the local **registry**
(`<config-dir>/servers/`, one JSON file per tenant). Every enroll and
un-enroll re-renders the *entire* relay configuration from that registry and
rewrites it wholesale on the relay — a stale or corrupted file on the relay
self-heals on the next operation.

## Enrolling a server

Enrollment is a two-file exchange between the server operator and the relay
admin. No credentials travel in either direction — the join request carries
only public material (CA *certificate*, SSH *public* key, UUID).

```mermaid
sequenceDiagram
    participant S as Server operator
    participant A as Relay admin
    participant R as Relay VM

    S->>S: tw server join-relay relay.example.com
    Note over S: writes tw_join_&lt;server-id&gt;.json
    S->>A: send join request file
    A->>A: tw relay enroll-server tw_join_&lt;id&gt;.json
    A->>R: write CA cert, rewrite Caddyfile + authorized_keys,<br/>live-add Xray inbound (no restart)
    Note over A: writes tw_join_response_&lt;id&gt;.json
    A->>S: send join response file
    S->>S: tw server join-relay --apply tw_join_response_&lt;id&gt;.json
    S->>R: tw server start — tunnel comes up
```

### On the server (first half)

```bash
tw server join-relay relay.example.com
```

This writes `tw_join_<server-id>.json` in the current directory. Send that
file to the relay admin. (`--new-context <name>` first creates and switches to
a fresh context, preserving the current one — useful when the machine already
serves another relay.)

### On the admin machine (the enrollment)

```bash
tw relay enroll-server tw_join_<server-id>.json
```

The enrollment runs five steps:

1. **Register** — the server is added to the registry and a unique reverse
   tunnel port is allocated (from 20000 upward). Enrolling the relay's own
   identity, or an already-enrolled server ID, is refused.
2. **Build the tenant list** — the admin's own slot plus every registered
   server.
3. **Apply relay config** — over the tunnelled SSH connection: each tenant's
   CA cert is written to `/etc/caddy/ca/<id>.crt`, the Caddyfile is
   re-rendered, **validated**, and gracefully reloaded (in-flight streams
   survive), `authorized_keys` is rewritten in full, and the full Xray
   `config.json` is persisted for reboots.
4. **Live enroll** — only the *new* tenant's inbound is hot-added to the
   running Xray via its API. **No Xray restart** — existing tenants' tunnels
   are never interrupted.
5. **Response** — `tw_join_response_<server-id>.json` is written. Send it back
   to the server operator.

!!! note "Enroll and un-enroll are serialized"
    Both operations render the full relay state from the registry and rewrite
    it wholesale, so they take a per-profile lock. Concurrent runs queue up
    rather than dropping each other's tenants.

### Back on the server (second half)

```bash
tw server join-relay --apply tw_join_response_<server-id>.json
tw server start
```

The response carries the admin-assigned coordinates (relay host, path,
port, SSH user) plus a signature binding the server's mode to its keypair.
`tw server start` brings the tunnel up.

## Listing tenants

```bash
tw relay get-servers
```

```
SERVER-ID          PATH                   PORT    ENROLLED           TUNNEL
srv-a1b2c3d4       /tw/srv-a1b2c3d4       20000   2026-07-12T09:41   up
srv-e5f6a7b8       /tw/srv-e5f6a7b8       20001   2026-07-20T18:03   down
```

The table combines the registry with **one live query against the relay**: a
tenant's tunnel is `up` iff the relay currently holds a listener on that
tenant's allocated port (i.e. the server's reverse SSH tunnel is established).
If the relay is unreachable the command **fails hard** rather than showing a
stale table.

## Un-enrolling a server

```bash
tw relay un-enroll-server <server-id> [--yes]
```

This removes the tenant **completely** — configuration and live state:

1. **Block re-auth** — `authorized_keys` is rewritten without the tenant's
   key, so it cannot re-establish anything.
2. **Sever live VLESS** — the tenant's inbound and routing rules are
   hot-removed from the running Xray, cutting the server transport and all of
   its clients immediately.
3. **Kill the reverse tunnel** — the sshd session still holding the tenant's
   listener port on the relay is terminated.
4. **Clean the config** — Caddyfile re-rendered, validated, gracefully
   reloaded; the tenant's CA cert removed from `/etc/caddy/ca/`; the Xray
   `config.json` persisted.
5. **Forget locally** — the registry entry is removed *last*, so a mid-way
   failure keeps the entry and the command can simply be **re-run** — every
   step is idempotent.

The command shows the target's identity (ID, port, enrollment time) and asks
for confirmation; `--yes` skips the prompt for scripts (the details still
print, so scripted runs log exactly what was removed). Un-enrollment is not a
ban: the same server can re-join later via a fresh
`tw server join-relay` exchange.

## Dashboard equivalents

The admin dashboard's **Servers** page mirrors all of this:

- the enrolled-servers table with the same columns (Server ID, Path, Port,
  Enrolled, Tunnel), with the tunnel state queried live from the relay;
- **Enroll a Server** — upload the `tw_join_*.json` file; when the enrollment
  completes, the join response downloads automatically, ready to send back;
- per-row **un-enroll**, with the same complete-removal semantics.

## Isolation properties

What a tenant *gets*:

| Per tenant | Enforced by |
| ---------- | ----------- |
| Own path `/tw/<server-id>` with its own Caddy handle block | Caddyfile, rendered per tenant |
| Own CA in the mTLS trust pool (`/etc/caddy/ca/<id>.crt`) | Caddy `client_auth require_and_verify` |
| Own VLESS UUID and Xray inbound | Relay Xray config |
| Own reverse-tunnel port (allocated at enrollment) | `permitlisten` in its `authorized_keys` line |

What a tenant *cannot* do on the relay:

- **No shell, no commands** — its `authorized_keys` line carries `restrict`
  (with only `port-forwarding` re-enabled), so it is forwarding-only: no
  shell, no exec, no agent or X11 forwarding.
- **No listening on other tenants' ports** — `permitlisten` pins its reverse
  forward to its own allocated port only.
- **No reaching relay-internal services** — local (`-L`) forwarding is pinned
  by `permitopen` to a dead sentinel port, so a tenant cannot dial the relay's
  Xray management API or any other loopback service.
- **No direct SSH from the internet** — tenant keys are always pinned
  `from="127.0.0.1"` (tunnel-only), even on `--ssh-open` relays.
- **No reading anyone's traffic** — all streams through the relay are
  end-to-end SSH-encrypted between client and server; the relay (and therefore
  the admin) only ever sees ciphertext.

The exact `authorized_keys` lines behind these guarantees are dissected on
[SSH access](ssh-access.md).
