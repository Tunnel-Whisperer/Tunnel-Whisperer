# API Reference

Tunnel Whisperer exposes two APIs: a **REST/WebSocket API** served by the
dashboard for browser and HTTP clients, and a **gRPC API** for CLI-to-daemon
communication.

---

## REST API (Dashboard)

The dashboard HTTP server registers the endpoints listed below. All REST
endpoints accept and return JSON unless noted otherwise.

### Read-only

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Current daemon status (mode, relay, server/client state) |
| `GET` | `/api/config` | Current configuration (sanitized) |
| `GET` | `/api/relay` | Relay provisioning status (provisioned, domain, IP, provider) |
| `GET` | `/api/providers` | List of supported cloud providers for relay provisioning |

### Mode

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/mode` | Set the operating mode (`server` or `client`) |

**Request body:**

```json
{ "mode": "server" }
```

### Settings

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/proxy` | Set or clear the outbound proxy URL |
| `POST` | `/api/log-level` | Set the log level (`debug`, `info`, `warn`, `error`) |

**Proxy request body:**

```json
{ "proxy": "socks5://host:1080" }
```

**Log level request body:**

```json
{ "level": "debug" }
```

!!! note "Restart required"
    Changing the log level persists the value to `config.yaml` and restarts
    the daemon process to apply the new level.

### Server control

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/server/start` | Start all server components (SSH, Xray, reverse tunnel) |
| `POST` | `/api/server/stop` | Stop the server |
| `POST` | `/api/server/restart` | Stop and restart the server |

### Client control

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/client/start` | Start the client (Xray + SSH tunnel) |
| `POST` | `/api/client/stop` | Stop the client |
| `POST` | `/api/client/reconnect` | Disconnect and reconnect the client |
| `POST` | `/api/client/upload` | Upload a user config bundle (`.zip`) to configure the client |

**Upload:** `POST /api/client/upload` expects a `multipart/form-data` body
with the zip file.

### Relay management

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/relay/test-creds` | Validate cloud provider credentials |
| `POST` | `/api/relay/provision` | Provision a new relay server via Terraform |
| `POST` | `/api/relay/destroy` | Destroy the provisioned relay server |
| `POST` | `/api/relay/test` | Run connectivity tests against the relay |
| `POST` | `/api/relay/generate-script` | Generate a manual setup script for the relay |
| `POST` | `/api/relay/save-manual` | Save relay details from a manual (non-Terraform) setup |
| `WS` | `/api/relay/ssh` | WebSocket-based interactive SSH shell to the relay server |

**Provision request body:**

```json
{
  "domain": "relay.example.com",
  "provider": "digitalocean",
  "token": "dop_v1_..."
}
```

!!! info "WebSocket: `/api/relay/ssh`"
    This endpoint upgrades to a WebSocket connection and provides a full
    interactive terminal session to the relay server. The dashboard uses
    [xterm.js](https://xtermjs.org/) to render the terminal in the browser.

### User management

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/users` | List all configured users |
| `POST` | `/api/users` | Create a new user |
| `DELETE` | `/api/users/{name}` | Delete a user by name |
| `GET` | `/api/users/{name}/download` | Download a user's config bundle as a `.zip` file |
| `POST` | `/api/users/apply` | Apply user changes (regenerate `authorized_keys`) |
| `POST` | `/api/users/unregister` | Unregister users from the server |
| `GET` | `/api/users/online` | List currently connected users |

**Create user request body:**

```json
{
  "name": "alice",
  "mappings": [
    { "client_port": 3389, "server_port": 3389 },
    { "client_port": 8443, "server_port": 443 }
  ]
}
```

**Download response:** `application/zip` binary with `Content-Disposition`
header.

### Server-Sent Events (SSE)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/events/{session_id}` | SSE stream of daemon events (status changes, progress) |
| `GET` | `/api/logs` | SSE stream of real-time log output |

The `{session_id}` parameter identifies a browser session so multiple
dashboard tabs can each receive events independently.

**Event format:**

```
event: status
data: {"mode":"server","server":{"state":"running",...}}

event: progress
data: {"step":2,"total":5,"label":"Starting Xray","status":"running"}
```

---

## gRPC API

The gRPC API listens on port **50051** (configurable via `server.api_port`)
and is used for CLI-to-daemon communication. When a daemon is running (via
`tw serve` or `tw dashboard`), CLI commands like `tw status`, `tw list users`,
and `tw delete user` connect to this API instead of reading state directly
from disk.

!!! note
    The gRPC API is an internal interface. It is not intended for external
    consumption and its protobuf schema may change between versions. Use the
    REST API for integrations.

### Available RPC methods

| Method | Description |
|---|---|
| `GetStatus` | Returns current mode, relay status, server/client state, user count |
| `ListUsers` | Returns all configured users with their tunnel mappings |
| `DeleteUser` | Deletes a user by name |
| `GetUserConfig` | Returns a user's config bundle as a zip byte stream |
| `TestRelay` | Runs relay connectivity tests and returns step-by-step results |
| `DestroyRelay` | Destroys the provisioned relay (accepts cloud credentials) |

The gRPC server starts automatically when running `tw serve` or
`tw dashboard`.
