# Configuration

Tunnel Whisperer uses a single YAML file for all settings. The same file
structure is used in every mode -- only the relevant sections are read
depending on the configured `mode`.

## Config file paths

| Platform | Path |
|---|---|
| Linux | `/etc/tw/config/config.yaml` |
| macOS | `/etc/tw/config/config.yaml` |
| Windows | `C:\ProgramData\tw\config\config.yaml` |

!!! tip "Override the config directory"
    Set `TW_CONFIG_DIR` (or pass the equivalent `--config-dir` flag, which
    wins over an inherited env value) to use a custom directory — no root
    needed:

    ```bash
    export TW_CONFIG_DIR=/opt/myapp/tw
    # Config file becomes /opt/myapp/tw/config.yaml
    ```

## Full annotated config

```yaml
# Operating mode: "relay", "server", or "client".
# Determines which commands are available and which services start.
# The legacy value "admin" is accepted on read and rewritten to "relay".
mode: server

# Log verbosity: debug, info, warn, error.
# Can also be set with --log-level flag (persisted on use).
log_level: info

# Log output format: "text" (default, human-readable) or "json".
# JSON maps attributes to OpenTelemetry semantic-convention names.
# Can also be set with --log-format flag (persisted on use).
log_format: text

# Outbound proxy for all connections (Xray, SSH, Terraform).
# Supported formats:
#   socks5://host:port
#   socks5://user:pass@host:port
#   http://host:port
#   http://user:pass@host:port
# Leave empty for direct connections.
proxy: ""

# Shared transport layer (used by all modes).
xray:
  # Profile identity UUID (VLESS client id). On a server/relay it also derives
  # the server-id (<hostname>-<first 8 hex of uuid>); on a client it is the
  # per-user UUID issued at user creation.
  uuid: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"

  # Domain or IP of the relay server.
  relay_host: relay.example.com

  # HTTPS port on the relay.
  relay_port: 443

  # XHTTP path used by Xray. Default /tw; rewritten to /tw/<server-id> when a
  # relay is provisioned or a join response is applied.
  path: /tw

  # X.509 client certificate presented to the relay's mutual-TLS gate.
  # Auto-populated at runtime from <config-dir>/client.{crt,key} when present,
  # so you normally leave these empty. See Security → Relay Authentication.
  client_cert_path: ""
  client_key_path: ""

# Server settings (used in server mode; the relay_ssh_* / remote_port fields
# are also used in relay mode for the relay SSH connection, and api_port is
# read in every mode to find a running daemon).
server:
  # Port the internal SSH server listens on.
  ssh_port: 2222

  # Port the gRPC API listens on (CLI-to-daemon communication).
  api_port: 50051

  # Port the web dashboard listens on.
  dashboard_port: 8080

  # SSH port on the relay server (for the reverse tunnel).
  relay_ssh_port: 22

  # SSH user on the relay server.
  relay_ssh_user: ubuntu

  # Remote port on the relay that maps back to the local SSH port.
  remote_port: 2222

  # Local port the embedded Xray instance listens on. Optional; leave 0
  # to use the built-in default.
  xray_port: 0

  # Port used for temporary Xray tunnel during relay config updates
  # (user creation, user registration). Change if 59000 is in use.
  temp_xray_port: 59000

  # Application templates — reusable port mapping bundles.
  # Used when creating or editing users to pre-fill port mappings.
  applications:
    - name: "web-app"
      mappings:
        - { client_port: 3000, server_port: 3000 }
        - { client_port: 5432, server_port: 5432 }

# Bandwidth analytics (opt-in, works in both modes).
analytics:
  enabled: true
  history_size: 720  # snapshots to keep (default 720 = 1h at 5s intervals)

# Client-only settings (ignored in server/relay mode).
client:
  # SSH user to authenticate as on the server.
  ssh_user: tunnel

  # SSH port on the server (matches server.ssh_port via the tunnel).
  server_ssh_port: 2222

  # Local port the embedded Xray dokodemo-door listens on (default 54001).
  xray_port: 54001

  # Local interface forwarded tunnels bind to.
  # 127.0.0.1 = local only (default); 0.0.0.0 = all interfaces (required
  # when running tw inside a container that publishes ports to the host).
  listen_address: 127.0.0.1

  # Port forwarding rules — each entry creates a local listener.
  tunnels:
    - local_port: 3389
      remote_host: 127.0.0.1
      remote_port: 3389
    - local_port: 8443
      remote_host: 127.0.0.1
      remote_port: 443

# Tamper-evidence signature over (mode, profile identity). Written by
# enrollment / bundle export / relay self-signing — never by hand.
mode_auth:
  sig: "base64-ed25519-signature"
  issuer: "base64-ed25519-public-key"
```

## Field reference

### Top-level fields

| Field | Type | Default | Description |
|---|---|---|---|
| `mode` | string | _(empty)_ | Operating mode: `relay`, `server`, or `client`. Legacy `admin` is migrated to `relay` on load. |
| `log_level` | string | `info` | Log verbosity. One of `debug`, `info`, `warn`, `error`. |
| `log_format` | string | `text` | Log output format. `text` (human-readable) or `json` (OpenTelemetry semantic-convention attribute names). Also set via `--log-format`. |
| `proxy` | string | _(empty)_ | Outbound proxy URL for all connections. |
| `mode_auth` | object | _(absent)_ | Detached ed25519 signature making `mode` tamper-evident (`sig` + `issuer`, both base64). A present-but-invalid signature makes role commands fail; an absent one is legacy-tolerated with a warning (a relay re-signs itself). Not a security boundary — see [Security](../security/index.md). |

### `xray` section

| Field | Type | Default | Description |
|---|---|---|---|
| `uuid` | string | _(empty)_ | Profile identity UUID (Xray VLESS client id). Per-user on clients; on servers/relays it also derives the server-id. |
| `relay_host` | string | _(empty)_ | Relay server domain or IP address. |
| `relay_port` | int | `443` | HTTPS port on the relay. |
| `path` | string | `/tw` | XHTTP path for the Xray transport. Becomes `/tw/<server-id>` once a relay is provisioned/joined. |
| `client_cert_path` | string | _(empty)_ | Path to the X.509 client certificate (PEM) presented to the relay's mutual-TLS gate. Auto-derived at runtime from `<config-dir>/client.crt` when present; rarely set by hand. See [Relay Authentication](../security/relay-authentication.md). |
| `client_key_path` | string | _(empty)_ | Path to the private key (PEM) for `client_cert_path`. Auto-derived from `<config-dir>/client.key`. |

### `server` section

| Field | Type | Default | Description |
|---|---|---|---|
| `ssh_port` | int | `2222` | Local SSH server listen port. |
| `api_port` | int | `50051` | gRPC API listen port. Read in every mode to locate a running daemon. |
| `dashboard_port` | int | `8080` | Web dashboard listen port. Set to `0` to disable. |
| `relay_ssh_port` | int | `22` | SSH port on the relay for the reverse tunnel. |
| `relay_ssh_user` | string | `ubuntu` | SSH user on the relay server. |
| `remote_port` | int | `2222` | Remote port on the relay forwarded back to local SSH. Enrolled servers get their port assigned by the relay admin (starting at 20000). |
| `xray_port` | int | _(empty)_ | Local port the embedded Xray instance listens on. Optional; leave unset to use the built-in default. |
| `temp_xray_port` | int | `59000` | Port for the temporary Xray tunnel used during relay config updates (user creation/registration). Change if `59000` is already in use on your system. |
| `applications` | list | _(empty)_ | Application templates — reusable port mapping bundles for user creation. |

### `applications[]` entry

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique name for the application template. |
| `mappings` | list | Port mapping rules. Each entry has `client_port` and `server_port`. |

### `applications[].mappings[]` entry

| Field | Type | Description |
|---|---|---|
| `client_port` | int | Port the client listens on locally (1-65535). |
| `server_port` | int | Port on the server to forward to (1-65535). |

### `analytics` section

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable bandwidth statistics collection. Opt-in. |
| `history_size` | int | `720` | Number of snapshots in the ring buffer. At the default 5-second interval, 720 = 1 hour of history. |

Analytics works in both server and client modes. In server mode, stats are tracked per user per port. In client mode, stats are tracked per local port. Changes via the dashboard take effect immediately without a restart.

### `client` section

| Field | Type | Default | Description |
|---|---|---|---|
| `ssh_user` | string | `tunnel` | SSH user to authenticate as on the server side. |
| `server_ssh_port` | int | `2222` | SSH port on the server (reached via the tunnel). |
| `xray_port` | int | `54001` | Local port the embedded Xray dokodemo-door listens on. |
| `listen_address` | string | `127.0.0.1` | Local interface forwarded tunnels bind to. Set to `0.0.0.0` to expose tunnels on all interfaces — required when `tw` runs inside a container that publishes ports to the host. Also settable with `tw client listen`. |
| `tunnels` | list | _(empty)_ | Port forwarding rules. Each entry has `local_port`, `remote_host`, `remote_port`. |

### `tunnels[]` entry

| Field | Type | Description |
|---|---|---|
| `local_port` | int | Port to listen on locally (client machine). |
| `remote_host` | string | Target host on the server side (usually `127.0.0.1`). |
| `remote_port` | int | Target port on the server side. |

## Editing the config

Prefer the CLI/dashboard over hand-editing: config mutations go through
validated setters that persist atomically (`tw proxy set`, `tw client
listen`, the dashboard settings pages). `tw config view` prints the active
file. Note that hand-editing the `mode` field breaks the `mode_auth`
signature and makes role commands fail.

## Config change detection

Tunnel Whisperer computes a **SHA-256 hash** of the config file at startup.
While the daemon is running, the dashboard periodically compares the current
file hash against the startup hash.

If they differ, the dashboard displays a notification indicating that the
configuration has changed and the server or client needs a restart for the
changes to take effect.

!!! info "Two hashing methods"
    - **Structured hash** (`Config.Hash()`) -- serializes the parsed config back
      to YAML and hashes the result. Detects changes to known fields.
    - **File hash** (`FileHash()`) -- hashes the raw file bytes on disk.
      Detects all changes including comments, formatting, and unknown fields.

    The file hash is the one used for change detection, so even cosmetic edits
    will trigger the notification.
