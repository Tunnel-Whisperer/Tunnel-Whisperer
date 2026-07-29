# File Layout

Tunnel Whisperer stores all runtime configuration, keys, and infrastructure
state under a single platform-specific directory.

| Platform | Base directory |
|---|---|
| Linux | `/etc/tw/config/` |
| macOS | `/etc/tw/config/` |
| Windows | `C:\ProgramData\tw\config\` |
| Override | `TW_CONFIG_DIR` environment variable, or the `--config-dir` flag |

---

## Common files (every mode)

| File | Description |
|---|---|
| `config.yaml` | Main configuration file (see [Configuration](configuration.md)) |
| `id_ed25519` / `id_ed25519.pub` | The profile's ed25519 SSH identity key pair, generated on first initialization. Server: authenticates the reverse tunnel to the relay. Relay: the admin management key. Client: the per-user key received in the bundle. Also the signing/identity key for `mode_auth`. |
| `contexts.yaml` | Plaintext context index: the active context name plus non-secret metadata (role, relay, user, short ID, created) per stored context |
| `contexts/<name>.twctx` | Sealed bundle of each **non-active** stored context (the active context lives unpacked in the config dir itself and is sealed on switch-away) |

## Server file tree

A server enrolled on a relay, with two users, has this layout:

```
/etc/tw/config/
├── config.yaml              # Main configuration file
├── contexts.yaml            # Context index
├── contexts/                # Sealed non-active contexts (*.twctx)
├── id_ed25519               # Server identity key (reverse tunnel auth)
├── id_ed25519.pub
├── authorized_keys          # SSH authorized keys (seeded with the server's own key; one entry per user, re-read on every auth)
├── ssh_host_ed25519_key     # Embedded SSH server host key (generated on first `tw server start`)
├── ca.crt                   # Per-server CA certificate (PEM); public cert shipped to the relay trust pool
├── ca.key                   # Per-server CA private key (PEM); never leaves the server
├── client.crt               # Client certificate (PEM) presented to the relay's mutual-TLS gate
├── client.key               # Private key (PEM) for client.crt
└── users/
    ├── alice/
    │   ├── config.yaml      # Client config pre-filled for this user (no mode field; mode is injected on export)
    │   ├── id_ed25519       # User's SSH private key
    │   ├── id_ed25519.pub   # User's SSH public key (mirrored into authorized_keys)
    │   ├── .applied         # Marker: user is registered on the relay (written by create/apply)
    │   ├── .single-session  # Optional marker: enforce one concurrent session for this user
    │   └── .mappings-dirty  # Optional marker: mappings changed since the bundle was last exported
    └── bob/
        └── ...
```

## Relay (admin) file tree

The relay owner's profile additionally holds the tenant registry and the
relay infrastructure state:

```
/etc/tw/config/
├── config.yaml              # mode: relay
├── contexts.yaml / contexts/
├── id_ed25519(.pub)         # Admin management key (authorized on the relay VM)
├── ca.crt / ca.key          # The relay profile's own tenant CA (the admin is also a tenant)
├── client.crt / client.key  # ...and its mTLS client certificate
├── servers/                 # Enrolled-server registry: one JSON file per tenant
│   └── <server-id>.json     # server_id, uuid, hostname, remote_port, ca_cert_pem, ssh_pubkey, enrolled_at
├── relay/                   # Relay infrastructure state (see below)
├── archive/
│   └── <domain>/caddy-certs.tar.gz   # Relay's Caddy TLS data, saved (best-effort) before destroy for reuse on re-provision
└── enroll.lock              # Transient lock serializing enroll/un-enroll runs (auto-removed; stale locks expire)
```

### The `relay/` directory

For a **cloud (Terraform) relay**, created by `tw relay create`:

| File | Description |
|---|---|
| `main.tf` | Terraform configuration for the selected provider (Hetzner, DigitalOcean, or AWS) |
| `cloud-init.yaml` | Cloud-init user data that installs Caddy + Xray and configures SSH |
| `terraform.tfvars` | Input variables (provider credentials/region), written `0600` |
| `terraform.tfstate` | Terraform state tracking the provisioned resources — its presence marks the relay as cloud-provisioned |
| `relay-meta.json` | Relay metadata (`ssh_open`, name) |

For a **manual (bring-your-own-VM) relay** there is no Terraform state; the
marker is:

| File | Description |
|---|---|
| `manual-relay.json` | Manual relay marker: domain, IP, `ssh_open` |

The generated install script itself (`tw-install-<domain>.sh`) is written to
the directory where you ran `tw relay create`, not the config dir.

!!! warning "Do not edit `terraform.tfstate`"
    The state file is managed by Terraform. Manual edits can cause resource
    drift or prevent clean destruction of the relay server.

## Client file tree

A client imports a context bundle (`tw config import <file> --activate`),
which unpacks into the config directory:

```
/etc/tw/config/
├── config.yaml              # Client configuration (mode: client, xray, tunnels, mode_auth)
├── contexts.yaml / contexts/
├── id_ed25519               # This user's SSH private key (from the bundle)
├── id_ed25519.pub
├── client.crt               # Per-server client certificate (from the bundle) for relay mTLS
└── client.key               # Private key for client.crt
```

!!! note "The client certificate is per-server, not per-user"
    `client.crt`/`client.key` admit the connection at the relay's mutual-TLS
    gate. Every user of the same server shares the same client certificate;
    individual identity is enforced later by the per-user SSH key. See
    [Relay Authentication](../security/relay-authentication.md).

---

## Context bundles (`.twctx`)

All portable identity is exchanged as `.twctx` bundles — a zip of profile
files sealed in the `TWBOX1` container format, **with no passphrase**:

| Bundle | Produced by | Contents |
|---|---|---|
| `tw_<name>.twctx` | `tw config export` (and automatically at the end of `tw relay create`) | The full profile of the exported context |
| `<name>-tw-context.twctx` | `tw config export-user <name>` (or the dashboard download) | A `role: client` context for one user: `config.yaml` (with `mode: client` and a `mode_auth` signature injected), `id_ed25519`, `id_ed25519.pub`, `client.crt`, `client.key` |

The client-side `config.yaml` inside a user bundle is pre-filled with:

- `mode: client` (plus the server-signed `mode_auth` block)
- `xray.uuid` — the user's unique UUID
- `xray.relay_host`, `xray.relay_port`, `xray.path` — transport settings for the server's relay slot
- `client.ssh_user` — the user's name
- `client.server_ssh_port` — matching the server's SSH port
- `client.tunnels` — the port mappings defined for the user

Certificate paths are **not** stored in the bundle config — they are derived
from the config dir at runtime, so a bundle works unchanged across platforms
and `TW_CONFIG_DIR` values.

!!! tip "Deploying a user bundle"
    ```bash
    tw config import alice-tw-context.twctx --activate
    tw client connect
    ```

!!! warning "Bundles carry no passphrase"
    A `.twctx` file is exactly as sensitive as the keys inside it. Transfer
    it over a trusted channel and delete stray copies.
