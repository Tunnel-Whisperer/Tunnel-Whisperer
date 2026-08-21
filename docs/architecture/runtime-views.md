# Runtime Views

## Relay Provisioning (`tw relay create`)

Interactive 9-step wizard, run on the admin machine (mode `relay`). Providers: Hetzner, DigitalOcean, AWS (via Terraform, which must be in PATH), or **Manual** — a bring-your-own VM set up with a tw-generated install script. `--provider manual --domain <d> --ip <ip>` runs fully non-interactive; `--ssh-open` optionally leaves port 22 open to the internet.

```mermaid
sequenceDiagram
    participant Admin as Admin (tw relay create)
    participant TF as Terraform
    participant Cloud as Cloud Provider
    participant R as Relay VM

    Admin ->> Admin: [1] Generate SSH keys (ed25519, if missing)
    Admin ->> Admin: [2] Generate Xray UUID (or reuse existing)
    Admin ->> Admin: [3] Set relay domain (xray.relay_host)
    Admin ->> Admin: [4] Select provider (Hetzner/DO/AWS/Manual)
    Admin ->> Admin: [5] Enter credentials (API token or AWS key pair)
    Admin ->> Admin: [6] Test credentials + confirm
    Admin ->> Admin: [7a] Render Caddyfile + Xray config + Terraform/cloud-init files
    Admin ->> TF: [7b] terraform init + apply
    TF ->> Cloud: Provision VM + firewall
    Cloud -->> TF: VM IP
    TF -->> Admin: relay_ip output
    Admin ->> Admin: [8] Prompt: create DNS A record, poll DNS until it resolves
    Admin ->> Admin: [9] Poll HTTPS until Caddy responds with valid TLS
    Note over R: cloud-init installs:<br/>Caddy, Xray, SSH lockdown, ufw
    R ->> R: Caddy issues TLS cert via Let's Encrypt
```

**Pre-check:** If a relay already exists (terraform.tfstate or manual-relay marker present), the wizard offers to destroy and recreate (non-interactive runs fail instead). For AWS, destruction requires re-entering credentials (passed via env vars). For Hetzner/DO, credentials are read from the existing `terraform.tfvars`.

**Credential testing:** Hetzner and DigitalOcean tokens are tested with a live API call (GET to their servers/account endpoint with Bearer auth). AWS credentials are format-checked (key ID length >= 16, secret length >= 30); full validation happens during `terraform apply`.

**File generation:** The wizard calls `terraform.Generate()` which renders:

- `cloud-init.yaml` -- from `cloud-init.yaml.tmpl` with baked values (domain, SSH user, public key, pinned Xray version, `--ssh-open` conditionals) plus the base64-embedded Caddyfile, relay Xray `config.json`, and CA certificate rendered by `internal/relay/{caddy,xray}`
- `main.tf` -- the selected provider's template (rendered for the `--ssh-open` firewall conditional)

For a **manual** relay, `GenerateManualInstallScript()` renders the same material into a self-contained shell script (`install-script.sh.tmpl`) the operator runs as root on their own Ubuntu VM; the script is idempotent and re-runnable.

Credentials are stored as:

- Hetzner/DO: `terraform.tfvars` in the relay directory (e.g. `hcloud_token = "..."`)
- AWS: passed via `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` environment variables to `terraform` commands

The cloud-init / install script on the relay:

1. Creates the SSH user with the admin's public key (pinned `from="127.0.0.1"` unless `--ssh-open`) and passwordless sudo
2. Installs Caddy from the official apt repo, Xray via the official install script pinned to `terraform.XrayVersion`
3. Writes the rendered Xray config (`api-in` on `127.0.0.1:10085`; the admin's own `vless-in-<id>` inbound on `127.0.0.1:<remote-port>+10000` with XHTTP; freedom outbound restricted to loopback via `finalRules`; per-tenant allow/deny routing rules)
4. Writes the admin's CA public certificate to `/etc/caddy/ca/<server-id>.crt` (base64 in cloud-init)
5. Writes the rendered Caddyfile — the mutual-TLS gate (`client_auth verify_if_given` against the CA trust pool, TLS 1.3 only) plus a per-tenant `handle` matching `/tw/<server-id>*` and cert `CN=<server-id>`, proxying h2c to the tenant's VLESS inbound, plus a certless `@enroll_<server-id>` handle for `/enroll/<tok>/*`
6. Locks down SSH to `127.0.0.1` only (`0.0.0.0` with `--ssh-open`), disables password authentication
7. Configures firewall: deny all incoming, allow 80/tcp + 443/tcp (+ 22/tcp with `--ssh-open`)

The Caddyfile, Xray config, and CA certificate are **rendered by tw** at provisioning
time (`internal/relay/{caddy,xray}` → `internal/ops/relay.go`) and embedded as base64,
so the relay boots with the mTLS gate already in place. See
[Relay Authentication](../security/relay-authentication.md).

---

## Server Enrollment (`tw relay invite` / `tw join`)

A second (or Nth) server joins an existing relay through a zero-file, spoken
invite: the admin mints a one-time code, reads it to the joining operator
over any channel, and the two sides run a SPAKE2 key exchange (keyed by the
code) tunnelled through the relay's `/enroll/<tok>` route — the same route a
certless connection can reach under `client_auth verify_if_given`. Neither
side's private key material ever crosses the wire; only the exchange itself
is protected by the derived session key, confirmed out-of-band by the two
humans reading back a short authentication string.

```mermaid
sequenceDiagram
    participant S as Joining Server
    participant A as Admin (relay role)
    participant R as Relay VM

    A ->> A: tw relay invite
    A ->> A: enroll.Mint(tok, ttl) — code = tok-NN-word-word
    Note over A: opens a listener on the relay via its own<br/>reverse SSH tunnel, serving /enroll/tok/*
    A ->> S: reads the code aloud (any channel)

    S ->> S: tw join relay.example.com &lt;code&gt;
    S ->> S: Generate identity (SSH key, CA, client cert CN=server-id)
    S ->> R: POST /enroll/tok/start, /offer (SPAKE2 + encrypted join-request payload)
    R ->> A: forwarded over the tunnel (relay sees only ciphertext)

    A ->> A: derives session key, computes SAS
    S ->> S: derives session key, computes SAME SAS
    A ->> A: shows SAS
    S ->> S: shows the same SAS
    S ->> A: reads SAS aloud
    A ->> A: confirms exact match, approves

    A ->> A: [1] Register server, allocate remote_port (registry under servers/)
    A ->> A: [2] Build FULL tenant list (admin + every registered server)
    A ->> R: [3] Over relay SSH: write CA certs, re-render + validate +<br/>graceful-reload Caddyfile, rewrite authorized_keys in full,<br/>persist full Xray config.json
    A ->> R: [4] gRPC live-add (AddInbound + AddRule via :10085<br/>over the SSH tunnel) — no Xray restart
    A ->> S: encrypted grant (relay host, path, remote_port, ssh_user,<br/>+ ed25519 mode signature), via /enroll/tok/result

    S ->> S: Store context (auto-activate if no live profile), persist coordinates + mode_auth signature
    S ->> R: tw server start — reverse tunnel on remote_port
```

Key properties:

- **The code is a PAKE password, not a bearer credential** (`internal/enroll`,
  SPAKE2) — the relay forwards only ciphertext between issuer and enrollee.
  The invite is single-use and burns on the *first* redemption attempt,
  successful or not, and expires after its TTL (default 15 minutes).
- **The SAS defeats theft and MITM** — both sides derive the same short
  authentication string from the session key (`internal/enroll/pake.go`,
  HKDF-SHA256); the admin approves only on an exact spoken match. A thief who
  redeems first burns the invite, so the real operator's join fails
  "already used" — the detectable tell.
- **Non-disruptive** — existing tenants' tunnels stay up: Caddy reloads gracefully, and the new Xray inbound + routing rules are added live over the gRPC API; the full `config.json` is written only for restart persistence.
- **Full-rewrite philosophy** — the Caddyfile, relay `authorized_keys`, and relay Xray config are re-rendered from the complete registry on every enroll/un-enroll, so stale or corrupted state self-heals.
- **Serialized** — enroll and un-enroll take a local file lock (`internal/ops/oplock.go`), so concurrent admin operations cannot interleave.
- **Tenant confinement** — each server's `authorized_keys` line carries
  `from="127.0.0.1",restrict,port-forwarding,permitopen=<dead sentinel>` plus
  **two** `permitlisten` entries: its tunnel port (`remote_port`) and its
  enroll port (`remote_port + 20000`, `enrollPort()` in `identity.go`) — it
  can publish exactly these two reverse listeners and nothing else (no shell,
  no reaching the relay's gRPC API).
- **Un-enroll** (`tw relay un-enroll-server <id>`) reverses everything *and* severs live state: rewrites `authorized_keys` first (blocks re-auth), removes the tenant's routing rules and inbound via gRPC, kills the sshd session holding its reverse listener, removes its Caddy handle and CA cert, then forgets the registry entry.
- **Two-phase delivery** (`internal/ops/invite.go`) — the grant is handed
  over on the same connection that served the offer where possible, but a
  second Caddy reload landing mid-exchange can kill that connection first;
  `deliverGrantPhase2` re-serves the same handler on fresh connections
  (retrying dial/listen failures) so the enrollee's next poll still collects
  the grant even then. The tenant is enrolled either way — an error here
  means "collect a grant that already exists," never a partial enrollment.

---

## User Enrollment (`tw server user invite`)

The only way a user comes to exist: the server operator mints a one-time
invite code and the client redeems it with `tw join`, running the same
SPAKE2 + SAS exchange as [server enrollment](#server-enrollment-tw-relay-invite-tw-join)
over the tenant's own `/enroll/<tok>` route.

```mermaid
sequenceDiagram
    participant Admin as Server operator (tw)
    participant C as Client machine (tw join)
    participant R as Relay (via management tunnel)
    participant AK as authorized_keys

    Admin ->> Admin: Enter username + port mappings<br/>(client local port -> server port, localhost only)
    Admin -->> C: One-time invite code (spoken — a SPAKE2 password)
    C ->> C: Generate ed25519 SSH key pair + CSR locally
    C ->> Admin: SSH public key + CSR + proposed UUID<br/>(SPAKE2-encrypted channel)
    Admin ->> Admin: SAS confirmation (both terminals, exact match)
    Admin ->> Admin: Sign the CSR with the server CA
    Admin ->> R: Hot-add UUID to this server's own<br/>vless-in-&lt;server-id&gt; inbound on the relay
    Admin ->> AK: Append public key with permitopen restrictions
    Admin ->> C: Signed cert + coordinates +<br/>mode_auth-signed config (encrypted channel)
```

The *client* generates its SSH key pair and certificate signing request
locally (`clientMaterial.makeOffer` in `internal/ops/joinflow.go`) — no
private key ever transits — and the server's `grantClient`
(`internal/ops/invite.go`) signs the CSR, registers the UUID, and writes
`authorized_keys` plus only the client's *public* key server-side. The client
stores the grant as a new context and connects with `tw client connect`.
There is no other creation path and no bundle file.

**Port mapping flow:** Ports are entered one mapping at a time in sequence. For each mapping, the wizard asks for the client's local port and the server port. The remote host is locked to `127.0.0.1` -- clients cannot forward to the server's wider network.

**Relay update mechanism** (`addUUIDToRelay` via `withRelaySSH`):

1. Starts a temporary management Xray instance (dokodemo-door on `server.temp_xray_port+1`, default 59001, falling back to any free loopback port) so it never conflicts with a running `tw server start`
2. SSHs into the relay through the temporary tunnel using the server's private key (the tunnel presents the server's mTLS client cert)
3. Reads `/usr/local/etc/xray/config.json` via `sudo cat`
4. Parses the JSON, adds the new UUID to the clients of this server's own `vless-in-<server-id>` inbound
5. Writes the updated config via `sudo tee` (persistence across Xray restarts)
6. Hot-adds the UUID via the Xray gRPC API (`AlterInbound` / `AddUserOperation` on `:10085`); falls back to `systemctl restart xray` if the API call fails

**Server-side files** in `<config_dir>/users/<name>/`:

- `config.yaml` -- copy of the granted client config (client UUID, relay host/port, the server's `/tw/<server-id>` path, tunnel mappings) — used by `user list`/`apply`
- `id_ed25519.pub` -- the client's enrolled SSH public key (the private half is born on the client machine and never leaves it)

The generated `authorized_keys` entry:

```text
permitopen="127.0.0.1:5432",permitopen="127.0.0.1:8080" ssh-ed25519 AAAA... alice@tw
```

This restricts the client to forwarding only to the specified `127.0.0.1` ports on the server. An optional `single-session` option (toggled per user) limits the user to one concurrent SSH session.

---

## Server Startup (`tw server start`)

```mermaid
sequenceDiagram
    participant S as Server
    participant X as Xray (in-process)
    participant R as Relay
    participant SSH as Embedded SSH

    S ->> S: Load config, ensure keys + certs + authorized_keys
    S ->> S: Save config file hash as cfgHash
    S ->> S: Start dashboard (if dashboard_port set)
    S ->> SSH: Start SSH server on :2222
    S ->> X: Start Xray (dokodemo :2223 -> relay SSH :22)
    S ->> R: SSH reverse tunnel via Xray (-R remote_port:localhost:2222)
    S ->> S: Start gRPC API on :50051
    Note over R: Relay now forwards 127.0.0.1:remote_port -> Server SSH
```

**Key generation:** On first run, `ensureKeys` generates an ed25519 SSH key pair (`id_ed25519` / `id_ed25519.pub`), an SSH host key (`ssh_host_ed25519_key`), seeds `authorized_keys` with the server's own public key, and (`ensureCerts`) creates the per-server CA and client certificate.

**Xray configuration:** The server-side Xray creates a dokodemo-door inbound on `sshPort+1` (default 2223, overridable via `server.xray_port`) that forwards to the relay's SSH port (default 22) via the VLESS+XHTTP+mTLS outbound on the server's `/tw/<server-id>` path.

**UUID auto-generation:** If `xray.uuid` is empty when the server starts with `relay_host` configured, a UUID is generated and saved to config automatically.

---

## Client Connection (`tw client connect`)

```mermaid
sequenceDiagram
    participant CL as Client
    participant X as Xray (in-process)
    participant R as Relay
    participant S as Server SSH

    CL ->> X: Start Xray (dokodemo :54001 -> relay :remote_port)
    CL ->> S: SSH via Xray tunnel (public key auth)
    S ->> S: Verify key in authorized_keys (re-read on every attempt)
    S ->> S: Parse permitopen restrictions
    CL ->> CL: Start local listeners (:5432, :8080, ...)
    Note over CL,S: Each connection: direct-tcpip -> server checks permitopen -> forwards to 127.0.0.1:port
```

**Multi-mapping over single session:** The client opens a single SSH session and creates multiple local listeners -- one per tunnel mapping. All port forwards share the same SSH connection. Listeners bind `client.listen_address` (default `127.0.0.1`; set `0.0.0.0` to expose tunnels, e.g. in containers).

**Client-side Xray:** The dokodemo-door inbound listens on `client.xray_port` (default 54001) and forwards to the server's remote SSH port on the relay (`client.server_ssh_port`, set from the server's admin-assigned `remote_port`).

---

## Client Reconnect

When the client detects a connection failure (SSH keepalive timeout or transport error), it performs a full teardown and rebuild:

```mermaid
sequenceDiagram
    participant Client as Client

    Client ->> Client: Stop port forwarding (close all local listeners)
    Client ->> Client: Stop Xray tunnel
    Client ->> Client: Reload config from disk
    Client ->> Client: Apply new log level
    Client ->> Client: Start Xray tunnel
    Client ->> Client: Start port forwarding
```

!!! note "Backoff"
    Reconnection uses stepped exponential backoff: 2s, 4s, 8s, 16s, up to a maximum of 30s between attempts (staying several attempts at each level). See [Cross-cutting Concerns](cross-cutting.md#auto-reconnection) for details.

---

## Data Flow (End-to-End)

```text
Client app                                                              Server service
    |                                                                         ^
    v                                                                         |
localhost:5432 --> SSH channel (direct-tcpip) ------------------> 127.0.0.1:5432
    |                        |                                        ^
    v                        v                                        |
Xray dokodemo    VLESS+XHTTP+mTLS :443       Xray freedom       SSH reverse tunnel
(:54001)         path /tw/<server-id>     (loopback only) --> (127.0.0.1:<remote_port>
                      Relay                     ^                on relay)
                  Caddy :443 ---------> vless-in-<server-id>          |
                  (mTLS gate,          (127.0.0.1:                    |
                   per-tenant handle)   <remote_port>+10000)   Server SSH :2222
```
