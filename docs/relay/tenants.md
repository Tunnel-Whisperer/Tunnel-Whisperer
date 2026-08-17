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

Enrollment is a **zero-file invite**: the admin mints a one-time code and
reads it to the server operator over any channel (phone, chat, whatever's
convenient); no request/response files ever change hands.

```mermaid
sequenceDiagram
    participant S as Server operator
    participant A as Relay admin
    participant R as Relay VM

    A->>A: tw relay invite
    Note over A: mints code &lt;tok&gt;-NN-word-word, waits
    A->>S: read the code aloud (any channel)
    S->>S: tw join relay.example.com &lt;code&gt;
    S->>R: SPAKE2 key exchange (code is the password)
    Note over S,A: both sides derive the same session key
    A->>A: shows SAS (e.g. "7F2-A91")
    S->>S: shows the SAME SAS
    S->>A: reads the SAS aloud
    A->>A: confirms exact match, approves
    A->>R: write CA cert, rewrite Caddyfile + authorized_keys,<br/>live-add Xray inbound (no restart)
    A->>S: encrypted grant (relay coords, mode signature)
    S->>S: stores a ready, mode-signed context (auto-activated)
    S->>R: tw server start — tunnel comes up
```

### On the admin machine

```bash
tw relay invite [--ttl 15m]
```

This mints a one-time invite code — `<tok>-NN-word-word`, from a curated
BIP-39 wordlist — and blocks, waiting for the enrollee. Read the code to the
server operator over **any** channel: it's a [SPAKE2](https://en.wikipedia.org/wiki/Password-authenticated_key_agreement)
password, not a bearer credential, so overhearing it alone doesn't grant
enrollment (see [Security](#security)).

### On the server (the new machine)

```bash
tw join relay.example.com <code> [--name <ctx>]
```

`tw join` works with **no mode configured yet** — it's the single entry point
for both roles, and the issuer's invite decides which one you get. Running it
against a `tw relay invite` code enrolls this machine as a **server** tenant.
Both terminals — the admin's `tw relay invite` and this machine's `tw join` —
now show a short authentication string (SAS), e.g. `7F2-A91`. **Read yours
aloud to the admin**; they approve only on an exact match.

Once approved, `tw join`:

1. **Generates identity** — an Xray UUID, an ed25519 SSH key pair, and a
   per-server CA plus client certificate for the relay's mutual-TLS gate, all
   locally on this machine.
2. **Runs the enrollment** — the admin's side executes the same five steps as
   before (register, build the tenant list, apply relay config, live-add the
   Xray inbound, done); nothing here changed except how the request/response
   travel.
3. **Stores the result** — a new, mode-signed context, named
   `--name` or auto-derived from the relay host. On a fresh machine with no
   active profile it's activated immediately; otherwise switch to it with
   `tw config use-context <name>`.

```bash
tw server start
```

!!! note "Enroll and un-enroll are serialized"
    Both operations render the full relay state from the registry and rewrite
    it wholesale, so they take a per-profile lock. Concurrent runs queue up
    rather than dropping each other's tenants.

## Security

- **The code is a PAKE password, not a bearer credential.** `tw join` and
  `tw relay invite` run [SPAKE2](https://en.wikipedia.org/wiki/Password-authenticated_key_agreement)
  over the code; the relay only ever forwards ciphertext between the two
  sides. Someone who overhears the spoken code still can't complete the
  exchange without also passing the SAS check below.
- **The SAS read-back defeats code theft and MITM.** Both sides derive the
  same short authentication string from the session key; the admin approves
  only when the enrollee reads back an exact match. If a thief redeems the
  invite first, they burn it — the real operator's `tw join` then fails with
  "already used", which is the tell that something is wrong.
- **Invites are single-use, short-lived, and burn on any attempt.** Default
  TTL is 15 minutes (configurable with `--ttl` on `tw relay invite`), and the
  invite is consumed by the *first* redemption attempt — including one with a
  wrong code that reaches the relay — not just a successful one.
- **Private keys never transit.** For server enrollment the SSH key and CA
  are generated locally by `tw join` and only public material crosses the
  wire, same as the old file-based handshake. For client enrollment
  (`tw server user invite`, see [Users](../server/users.md)) it goes
  further: the client generates its own SSH key *and* a certificate signing
  request locally, and the server signs the CSR — the client's private key
  never leaves the client machine at all.

## Same machine? Self-enrollment

When the server you want to enroll **is the admin machine itself** (the
single-operator setup: one person runs relay, server, and client), skip the
file exchange — from the relay context:

```bash
tw relay add-server [<context-name>] [--switch]
```

One command performs the whole handshake in-process: it generates a fresh
server identity (UUID, SSH keypair, CA, client certificate) in memory, runs
the same five enrollment steps as `tw relay invite` / `tw join` (register,
build tenant list, apply relay config, live-add the Xray inbound, done),
signs the new profile's mode with the relay key, and stores the result as a
new context (default name `server-<relay's first DNS label>`). Then:

```bash
tw config use-context server-relay   # or pass --switch above
tw server start
```

Notes:

- The active relay context is never modified, and no `tw_join_*.json` files
  are written.
- The new context is **born mode-signed** — none of the "mode is unsigned"
  warnings that appear mid-way through the file-based handshake.
- If the relay context uses the default dashboard/API ports, the new context
  gets free ones (printed on creation), so both contexts' daemons can run
  side by side on the one machine.
- If storing the context fails after the tenant was enrolled, the enrollment
  is rolled back automatically; the error names the tenant to remove with
  `tw relay un-enroll-server` if even the rollback fails.

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
ban: the same server can re-join later via a fresh `tw relay invite` / `tw
join` exchange.

## Upgrading an existing relay

Relays provisioned before invite-based enrollment shipped keep working for
already-enrolled tenants without any action — but **no invite of either kind
works yet**, including `tw relay invite` for a new server. A pre-upgrade
render never wrote the admin's own `/enroll/<tok>` route into the Caddyfile,
and `tw relay invite` has no other way to become reachable: the exchange is
served over that exact route, so there's no invite that can bootstrap it into
existence. (Same root cause as the tenant side: the enroll port is a second
`permitlisten` on each tenant's `authorized_keys` line — tunnel port plus
20000 — that a pre-upgrade render never wrote either.)

One **full render** fixes both at once, and it has to be triggered by
something that doesn't itself depend on the missing `/enroll` route:

- If no tenant is enrolled yet, run `tw relay add-server` — it enrolls a
  tenant entirely in-process over the admin's own management SSH connection,
  never through the public `/enroll` endpoint, so it isn't blocked by the
  thing it's fixing. Delete the resulting context (`tw config delete-context
  <name>`) or `tw relay un-enroll-server <its-id>` afterwards if you didn't
  actually want a self-enrolled server.
- If a tenant already exists, `tw relay un-enroll-server <server-id>` on any
  one of them works just as well — un-enrollment also renders the relay's
  full state (admin slot included) directly over SSH, not through `/enroll`.

Either one re-renders the Caddyfile and every tenant's `authorized_keys` from
the registry, adding the admin's `/enroll` route and every tenant's enroll
`permitlisten` in the same pass. After that, both `tw relay invite` and
`tw server user invite` work normally. Relays provisioned by this
version of `tw` already have both from the start and need nothing.

## Dashboard equivalents

The admin dashboard's **Servers** page mirrors the read side of this:

- the enrolled-servers table with the same columns (Server ID, Path, Port,
  Enrolled, Tunnel), with the tunnel state queried live from the relay;
- per-row **un-enroll**, with the same complete-removal semantics.

Enrollment itself (`tw relay invite`, `tw server user invite`) is
CLI-only for now — the human-in-the-loop SAS confirmation doesn't yet have a
dashboard equivalent.

## Isolation properties

What a tenant *gets*:

| Per tenant | Enforced by |
| ---------- | ----------- |
| Own path `/tw/<server-id>` with its own Caddy handle block | Caddyfile, rendered per tenant |
| Own CA in the mTLS trust pool (`/etc/caddy/ca/<id>.crt`) | Caddy `client_auth verify_if_given` + per-tenant CN match |
| Own VLESS UUID and Xray inbound | Relay Xray config |
| Own reverse-tunnel port (allocated at enrollment) | `permitlisten` in its `authorized_keys` line |

What a tenant *cannot* do on the relay:

- **No shell, no commands** — its `authorized_keys` line carries `restrict`
  (with only `port-forwarding` re-enabled), so it is forwarding-only: no
  shell, no exec, no agent or X11 forwarding.
- **No listening on other tenants' ports** — `permitlisten` pins its reverse
  forwards to its own two allocated ports (tunnel + enroll) only.
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
