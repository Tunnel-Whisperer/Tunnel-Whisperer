# Configuration

Tunnel Whisperer uses a single YAML file for all settings. The same file
structure is used on both server and client -- only the relevant sections are
read depending on the configured `mode`.

## Config file paths

| Platform | Path |
|---|---|
| Linux | `/etc/tw/config/config.yaml` |
| macOS | `/etc/tw/config/config.yaml` |
| Windows | `C:\ProgramData\tw\config\config.yaml` |

!!! tip "Override with environment variable"
    Set `TW_CONFIG_DIR` to use a custom directory:

    ```bash
    export TW_CONFIG_DIR=/opt/myapp/tw
    # Config file becomes /opt/myapp/tw/config.yaml
    ```

## Full annotated config

```yaml
# Operating mode: "server" or "client".
# Determines which commands are available and which services start.
mode: server

# Log verbosity: debug, info, warn, error.
# Can also be set with --log-level flag (persisted on use).
log_level: info

# Outbound proxy for all connections (Xray, SSH, Terraform).
# Supported formats:
#   socks5://host:port
#   socks5://user:pass@host:port
#   http://host:port
#   http://user:pass@host:port
# Leave empty for direct connections.
proxy: ""

# Shared transport layer (used by both server and client).
xray:
  # Xray client UUID — unique per user, generated during user creation.
  uuid: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"

  # Domain or IP of the relay server.
  relay_host: relay.example.com

  # Port for the HTTPS/WebSocket connection to the relay.
  relay_port: 443

  # WebSocket path used by Xray.
  path: /tw

# Server-only settings (ignored in client mode).
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

# Client-only settings (ignored in server mode).
client:
  # SSH user to authenticate as on the server.
  ssh_user: tunnel

  # SSH port on the server (matches server.ssh_port via the tunnel).
  server_ssh_port: 2222

  # Port forwarding rules — each entry creates a local listener.
  tunnels:
    - local_port: 3389
      remote_host: 127.0.0.1
      remote_port: 3389
    - local_port: 8443
      remote_host: 127.0.0.1
      remote_port: 443
```

## Field reference

### Top-level fields

| Field | Type | Default | Description |
|---|---|---|---|
| `mode` | string | _(empty)_ | Operating mode. Set to `server` or `client`. |
| `log_level` | string | `info` | Log verbosity. One of `debug`, `info`, `warn`, `error`. |
| `proxy` | string | _(empty)_ | Outbound proxy URL for all connections. |

### `xray` section

| Field | Type | Default | Description |
|---|---|---|---|
| `uuid` | string | _(empty)_ | Xray VLESS UUID. Generated per user during creation. |
| `relay_host` | string | _(empty)_ | Relay server domain or IP address. |
| `relay_port` | int | `443` | HTTPS/WebSocket port on the relay. |
| `path` | string | `/tw` | WebSocket path for the Xray transport. |

### `server` section

| Field | Type | Default | Description |
|---|---|---|---|
| `ssh_port` | int | `2222` | Local SSH server listen port. |
| `api_port` | int | `50051` | gRPC API listen port. |
| `dashboard_port` | int | `8080` | Web dashboard listen port. Set to `0` to disable. |
| `relay_ssh_port` | int | `22` | SSH port on the relay for the reverse tunnel. |
| `relay_ssh_user` | string | `ubuntu` | SSH user on the relay server. |
| `remote_port` | int | `2222` | Remote port on the relay forwarded back to local SSH. |
| `temp_xray_port` | int | `59000` | Port for the temporary Xray tunnel used during relay config updates (user creation/registration). Change if `59000` is already in use on your system. |
| `applications` | list | _(empty)_ | Application templates — reusable port mapping bundles for user creation. |

### `applications[]` entry

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique name for the application template (alphanumeric, dashes, underscores). |
| `mappings` | list | Port mapping rules. Each entry has `client_port` and `server_port`. |

### `applications[].mappings[]` entry

| Field | Type | Description |
|---|---|---|
| `client_port` | int | Port the client listens on locally (1-65535). |
| `server_port` | int | Port on the server to forward to (1-65535). |

### `client` section

| Field | Type | Default | Description |
|---|---|---|---|
| `ssh_user` | string | `tunnel` | SSH user to authenticate as on the server side. |
| `server_ssh_port` | int | `2222` | SSH port on the server (reached via the tunnel). |
| `tunnels` | list | _(empty)_ | Port forwarding rules. Each entry has `local_port`, `remote_host`, `remote_port`. |

### `tunnels[]` entry

| Field | Type | Description |
|---|---|---|
| `local_port` | int | Port to listen on locally (client machine). |
| `remote_host` | string | Target host on the server side (usually `127.0.0.1`). |
| `remote_port` | int | Target port on the server side. |

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
