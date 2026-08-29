# Access Control

Tunnel Whisperer enforces access control at three scopes. **Relay admission** is per-server, decided at the TLS handshake by a mutual-TLS client certificate. **User authorization** is per-user, decided on the server by the SSH public key and its port restrictions. **Relay SSH** is a separate, tightly restricted management surface. A connection must clear the certificate gate to reach the relay at all, and then clear the SSH key check to do anything on the server.

---

## Relay Admission — Mutual TLS (per server)

The relay's Caddy front door is configured with `client_auth verify_if_given`: a *presented* certificate must be signed by an enrolled server's own certificate authority or the TLS handshake is rejected, but a bare handshake with no certificate is allowed — it only ever reaches the enrollment endpoint or a uniform 404. Every **tunnel** route additionally requires a verified certificate whose CN matches that tenant, so no connection reaches a server's upstream without one.

- The certificate is **per server, not per user** — every user of a server shares the same `client.crt`/`client.key`, delivered in the user's context bundle.
- Admission is decided at the handshake, shielding the relay's downstream machinery from anonymous traffic.
- The certificate subject (`CN=<server-id>`) also **routes** the connection: it can only reach that server's own path and upstream on a multi-tenant relay.
- This is the **primary** transport-layer gate.

This layer is covered in full — CA generation, certificate distribution, the Caddy configuration, and the Xray-core mTLS support that presents the certificate — on the [Relay Authentication](relay-authentication.md) page.

---

## Transport-Layer Identity — Xray UUID (defense-in-depth)

Each user is assigned a unique **Xray UUID** at `tw server user invite` enrollment (the enrollee generates it; the server registers it). The UUID is registered under the server's own tenant inbound in the relay's Xray configuration (`/usr/local/etc/xray/config.json`) — live via the relay Xray API where possible, and persisted in the config file.

- Each UUID maps to a single user
- Removing a UUID from the relay (`tw server user unregister`, or automatically on `tw server user delete`) prevents that user from establishing a tunnel through it
- `tw server user apply` re-registers users' UUIDs (all, or by name) — useful after moving to a fresh relay

```json
{
  "inbounds": [{
    "tag": "vless-in-<server-id>",
    "settings": {
      "clients": [
        { "id": "server-uuid-here" },
        { "id": "alice-uuid-here" },
        { "id": "bob-uuid-here" }
      ]
    }
  }]
}
```

!!! info "No longer the security boundary"
    Admission is decided by the mutual-TLS certificate gate above. A caller
    that cannot present a trusted client certificate never reaches the point
    where the UUID is checked, so the UUID functions as defense-in-depth rather
    than the primary control.

---

## SSH Public Key Auth

All SSH authentication uses **Ed25519 key pairs**. Password authentication is disabled entirely.

- Each user's key pair is generated on their own machine during `tw server user invite` enrollment — the server only ever holds the public half
- The server maintains an `authorized_keys` file (in its config directory) with one entry per user, tagged with a `<name>@tw` comment
- The embedded SSH server (Go `x/crypto/ssh`) validates the client's public key against this file on every connection attempt

!!! warning "No password fallback"
    The SSH server does not support password authentication under any circumstances. If a client loses their private key, delete and re-create the user (which generates a fresh key pair and removes the old entry).

---

## Per-User Port Restrictions

The `permitopen` options in each `authorized_keys` entry limit the user to forwarding traffic only to specific `127.0.0.1:<port>` targets. Users cannot access the server's wider network or forward to arbitrary ports.

```
permitopen="127.0.0.1:5432",permitopen="127.0.0.1:8080" ssh-ed25519 AAAA... alice@tw
```

In this example, user `alice` can forward to:

- `127.0.0.1:5432` (e.g., PostgreSQL)
- `127.0.0.1:8080` (e.g., a web application)

Any `direct-tcpip` request to a destination not listed in `permitopen` is rejected by the SSH server.

!!! info "Localhost only"
    The remote host in port forwarding is locked to `127.0.0.1`. Users cannot specify external hosts — all forwarded traffic targets services running on the server machine itself.

### Single-session enforcement

An entry may additionally carry the custom `single-session` option. The embedded SSH server then rejects a second concurrent connection for that user while one is active. It is toggled per user (dashboard, or the API) and stored as a `.single-session` marker in the user's directory so it survives `authorized_keys` rewrites.

---

## Dynamic Authorization

The embedded SSH server **re-reads `authorized_keys` on every authentication attempt**. This means:

- Adding a new user takes effect immediately — no restart of the server required
- Revoking a user's key takes effect on the next connection attempt
- There is no cached state that could allow a revoked key to authenticate

This design ensures that access control changes are applied in real time, without service interruption.

---

## Relay SSH access control

The relay's own SSH daemon serves only two kinds of principals, both written into the relay's `authorized_keys` by `tw` (the file is fully re-rendered on every enroll/un-enroll, so it self-heals):

**The admin key** (the relay owner's `id_ed25519.pub`):

```
from="127.0.0.1" ssh-ed25519 AAAA... 
```

- Pinned `from="127.0.0.1"`: it only authenticates for connections arriving through the encrypted tunnel (which egress at localhost on the relay) — never over public port 22.
- **Exception:** a relay provisioned with `--ssh-open` leaves the admin line unpinned, because the pin would reject logins over the deliberately opened port 22. Re-provisioning without `--ssh-open` re-pins it.
- The admin key is otherwise unrestricted — only the admin can open a shell (`tw relay ssh`) or forward to relay loopback services.

**Tenant (enrolled server) keys** — one line per enrolled server:

```
from="127.0.0.1",restrict,port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:20000",permitlisten="127.0.0.1:40000" ssh-ed25519 AAAA...
```

- `from="127.0.0.1"` — tunnel-only, always (regardless of `--ssh-open`).
- `restrict` — no shell, no exec, no agent/X11 forwarding.
- `port-forwarding` — re-enables forwarding (which `restrict` alone denies); forwarding is all a tenant may do.
- `permitlisten="127.0.0.1:<port>"` (appears twice) — the reverse (`-R`) forwards are limited to the tenant's **own** two allocated ports: its tunnel port, and the tunnel port + 20000 (the enroll port `tw server user invite` publishes its client-enrollment listener on).
- `permitopen="127.0.0.1:1"` — local (`-L`) forwarding is pinned to a dead sentinel port, so a tenant cannot reach the relay's loopback services (e.g. the Xray gRPC API on `127.0.0.1:10085`) or other tenants' ports.

---

## User Revocation

**Revoke tunnel access (keep the user):**

```bash
tw server user unregister <name>
```

Removes the user's UUID from the relay's Xray configuration.

**Delete the user entirely:**

```bash
tw server user delete <name>
```

Removes the UUID from the relay, deletes the user's directory (keys, config), and removes their `authorized_keys` entry — blocking SSH authentication on the next attempt.

**Revoke a whole server** (relay admin):

```bash
tw relay un-enroll-server <server-id>
```

Removes the server's CA from the relay trust pool, its `authorized_keys` line, and its Xray tenant, and kills its live connections.

!!! warning "Defense-in-depth"
    Removing either the SSH key or the UUID is sufficient to block a user; `tw server user delete` does both. Revocation takes effect on the **next connection attempt** — an existing SSH session is not force-terminated, but it fails on reconnection.

---

## Management-Plane Access — the dashboard and control API

Two local management surfaces control the daemon, and both are gated by a bearer token so that reaching the port is not enough to drive them.

- **Web dashboard** — requires a login token (see [Dashboard → Signing in](../global/dashboard.md#signing-in)). Every route except the login page and static assets needs either a `SameSite=Strict`, `HttpOnly` session cookie or an `Authorization: Bearer` header; `/metrics` is gated too. The token lives in a `0600` file; `tw dashboard token --rotate` invalidates all sessions. The dashboard binds loopback by default and **refuses** an off-loopback bind over cleartext unless `server.dashboard_allow_lan` is explicitly set (then only behind a TLS terminator).
- **gRPC control API** (`tw server start`, `127.0.0.1:50051`) — bound to loopback **and** requires a per-daemon bearer token on every RPC, held in a separate `0600` file (`api.token`). Loopback is not treated as an auth boundary: a local process that cannot read the operator's token file cannot overwrite keys or config, flip mode, delete users, or read secrets through the API. The CLI reads the token from the shared config directory automatically.

---

## Compliance Properties

Tunnel Whisperer's access control model supports the following compliance-relevant properties:

### Zero plaintext data leaves the local network

All application data is encrypted by the SSH layer before it reaches the Xray tunnel. The TLS layer provides an additional envelope. No plaintext data is ever transmitted over the public internet.

### No credentials stored on relay

The relay stores only Xray UUIDs (transport identifiers), SSH **public** keys, and the **public** CA certificate(s) it trusts for admission. It does not store SSH private keys, CA signing keys, user passwords, or application data. **Compromise of the relay does not expose user credentials or data.**

### Principle of least privilege

Each user can only forward traffic to the specific `127.0.0.1:<port>` targets explicitly listed in their `permitopen` directives; each enrolled server can only use the relay for forwarding, scoped to its own port. There is no broad network access — every principal is scoped to the minimum required services.

### Audit trail via configurable logging

Tunnel Whisperer uses Go's `slog` structured logging framework. Log levels are configurable at runtime (`--log-level`, persisted; or the dashboard), and log output captures:

- SSH authentication attempts (success and failure)
- Port forwarding requests (allowed and denied)
- Tunnel establishment and disconnection events
- Xray connection lifecycle events
