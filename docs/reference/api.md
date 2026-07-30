# API Reference

Tunnel Whisperer exposes two APIs: a **REST/WebSocket/SSE API** served by the
dashboard for browser and HTTP clients, and a **gRPC API** for CLI-to-daemon
communication.

---

## REST API (Dashboard)

The dashboard HTTP server registers the endpoints listed below. All REST
endpoints accept and return JSON unless noted otherwise.

### Read-only

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Current daemon status (mode, version, relay, server/client state) |
| `GET` | `/api/config` | Current configuration |
| `GET` | `/api/relay` | Relay provisioning status (provisioned, domain, IP, provider, ssh_open) |
| `GET` | `/api/providers` | List of supported cloud providers for relay provisioning |
| `GET` | `/api/stats` | Bandwidth statistics snapshots and history (returns `enabled: false` when analytics is off) |
| `GET` | `/metrics` | Prometheus-format bandwidth metrics |

### Contexts

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/config/contexts` | List stored contexts (name, role, user, relay, id, current) |
| `POST` | `/api/config/use-context` | Switch the daemon's active context and reconnect. Body: `{ "name": "..." }` |

### Mode

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/mode` | Set the operating mode |

**Request body:**

```json
{ "mode": "server" }
```

### Settings

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/proxy` | Set or clear the outbound proxy URL |
| `POST` | `/api/log-level` | Set the log level (`debug`, `info`, `warn`, `error`) |
| `POST` | `/api/settings/server` | Update server settings (ports, relay SSH user, temp Xray port) |
| `POST` | `/api/settings/xray` | Update Xray transport settings (relay host, port, path) |
| `POST` | `/api/settings/client` | Update client settings (SSH user, server SSH port, Xray port, listen address) |
| `POST` | `/api/settings/analytics` | Enable/disable analytics and set history size |

**Proxy request body:**

```json
{ "proxy": "socks5://host:1080" }
```

**Log level request body:**

```json
{ "log_level": "debug" }
```

**Server settings request body:**

```json
{
  "ssh_port": 2222,
  "api_port": 50051,
  "dashboard_port": 8080,
  "relay_ssh_port": 22,
  "relay_ssh_user": "ubuntu",
  "remote_port": 2222,
  "xray_port": 54001,
  "temp_xray_port": 59000
}
```

Only non-zero / non-empty fields are applied; omitted fields keep their current value.

**Xray settings request body:**

```json
{
  "relay_host": "relay.example.com",
  "relay_port": 443,
  "path": "/tw"
}
```

**Client settings request body:**

```json
{
  "ssh_user": "tunnel",
  "server_ssh_port": 2222,
  "xray_port": 54001,
  "listen_address": "127.0.0.1"
}
```

`xray_port` and `listen_address` are optional. `listen_address` sets the local
interface forwarded tunnels bind to (`0.0.0.0` to expose them on all interfaces).

**Analytics settings request body:**

```json
{
  "enabled": true,
  "history_size": 720
}
```

Analytics changes take effect immediately — no restart required. The stats
collector is created or destroyed on the fly.

!!! note "Restart required"
    Settings changes are persisted to `config.yaml` immediately. A restart
    (server) or reconnect (client) is needed for most changes to take effect.
    **Exception:** analytics settings take effect immediately.

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
| `POST` | `/api/client/upload` | Upload a client context bundle (`.twctx`) to configure the client |

**Upload:** `POST /api/client/upload` expects a `multipart/form-data` body
with the bundle in a `config` file field (10 MB max).

### Relay management

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/relay/test-creds` | Validate cloud provider credentials |
| `POST` | `/api/relay/provision` | Provision a new relay server via Terraform |
| `POST` | `/api/relay/destroy` | Destroy the provisioned relay server |
| `POST` | `/api/relay/test` | Run connectivity tests against the relay |
| `POST` | `/api/relay/generate-script` | Generate the manual install script for a bring-your-own-VM relay |
| `POST` | `/api/relay/save-manual` | Save relay details from a manual (non-Terraform) setup |
| `WS` | `/api/relay/ssh` | WebSocket-based interactive SSH shell to the relay server |
| `POST` | `/api/relay/close-ssh` | Close the interactive relay SSH session |

!!! info "WebSocket: `/api/relay/ssh`"
    This endpoint upgrades to a WebSocket connection and provides a full
    interactive terminal session to the relay server. The dashboard uses
    [xterm.js](https://xtermjs.org/) to render the terminal in the browser.

### Enrolled servers (relay mode only)

These endpoints mirror the `tw relay …` enrollment commands and answer only
when the daemon runs in relay mode.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/servers` | List enrolled servers with live tunnel state (`tw relay get-servers`) |
| `POST` | `/api/servers/enroll` | Enroll a server: `multipart/form-data` upload of the join-request JSON in a `request` field; the join-response JSON is returned as an attachment (`tw_join_response_<server-id>.json`) |
| `POST` | `/api/servers/unenroll` | Un-enroll a server. Body: `{ "server_id": "..." }` |

### User management

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/users` | List all configured users |
| `POST` | `/api/users` | Create a new user |
| `DELETE` | `/api/users/{name}` | Delete a user by name |
| `PUT` | `/api/users/{name}/mappings` | Update a user's port mappings |
| `POST` | `/api/users/{name}/single-session` | Enable/disable the user's single-session flag |
| `GET` | `/api/users/{name}/download` | Download the user's client context bundle (`{name}-tw-context.twctx`) |
| `POST` | `/api/users/apply` | Register users on the relay. Body: `{ "names": [...] }` (empty = all) |
| `POST` | `/api/users/unregister` | Unregister users from the relay |
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

**Update user mappings request body:**

```json
{
  "mappings": [
    { "client_port": 3389, "server_port": 3389 },
    { "client_port": 8443, "server_port": 443 }
  ]
}
```

After updating, the user's `authorized_keys` entry is rewritten with new
`permitopen` restrictions and a mappings-dirty flag is set. The flag is
cleared when the user's bundle is downloaded.

**Download response:** `application/octet-stream` binary with
`Content-Disposition` header (a sealed `.twctx` context bundle, no
passphrase).

### Application templates

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/apps` | List all application templates |
| `POST` | `/api/apps` | Create a new application template |
| `PUT` | `/api/apps/{name}` | Update an application template |
| `DELETE` | `/api/apps/{name}` | Delete an application template |

**Create/update application request body:**

```json
{
  "name": "web-app",
  "mappings": [
    { "client_port": 3000, "server_port": 3000 },
    { "client_port": 5432, "server_port": 5432 }
  ]
}
```

Application templates are reusable port mapping bundles. They are stored in
`config.yaml` under `server.applications` and are not synced to the relay.

### Server-Sent Events (SSE)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/events/{session_id}` | SSE stream of daemon events (status changes, progress) |
| `GET` | `/api/logs` | SSE stream of real-time log output |

The `{session_id}` parameter identifies a browser session so multiple
dashboard tabs can each receive events independently.

**Event format** — unnamed `data:` frames only (no `event:` field). Each frame
is one `ProgressEvent` (or, on `/api/logs`, one log entry):

```
data: {"step":2,"total":5,"label":"Starting Xray","status":"running"}
```

Daemon status is **polled** via `GET /api/status`, not streamed.

---

## gRPC API

The gRPC API listens on port **50051** (configurable via `server.api_port`)
and is used for CLI-to-daemon communication. It starts automatically with
`tw server start` and `tw dashboard`. When a daemon is running, CLI commands
like `tw status`, `tw server user list`, and `tw config export-user` connect
to this API instead of reading state directly from disk.

### JSON codec — the proto is documentation only

The API uses the gRPC server machinery, but **the wire format is JSON, not
protobuf**. A custom codec (registered under the content-subtype `json`)
marshals hand-written Go structs directly, so no protoc-generated code is
involved. The file `proto/api/v1/service.proto` exists as documentation only;
`make proto` regenerates stubs that are not used on the wire. The service is
registered as `api.v1.TunnelWhisperer` and every RPC is unary.

Clients must therefore dial with the JSON call option (the built-in client
does: `grpc.CallContentSubtype("json")`, plaintext, 2-second dial timeout).

!!! note
    The gRPC API is an internal interface. Its message shapes may change
    between versions. Use the REST API for integrations.

### Service: `api.v1.TunnelWhisperer`

| Method | Request → Response | Description |
|---|---|---|
| `GetStatus` | `Empty` → `StatusResponse` | Mode, version, relay status, user count, connected-user count, server/client component state |
| `GetConfig` | `Empty` → `ConfigResponse` | The current on-disk configuration |
| `SetMode` | `SetModeRequest` → `Empty` | Set the operating mode |
| `ListProviders` | `Empty` → `ListProvidersResponse` | Supported cloud providers for relay provisioning |
| `GetRelayStatus` | `Empty` → `RelayStatusResponse` | Relay provisioning/connection status |
| `TestCredentials` | `TestCredentialsRequest` → `Empty` | Validate cloud-provider credentials |
| `ProvisionRelay` | `ProvisionRelayRequest` → `ProvisionRelayResponse` | Provision a relay VM via Terraform |
| `DestroyRelay` | `DestroyRelayRequest` → `Empty` | Destroy the relay (accepts cloud credentials map) |
| `TestRelay` | `Empty` → `TestRelayResponse` | Run relay connectivity tests; returns per-step results |
| `StartServer` / `StopServer` | `Empty` → `Empty` | Start/stop all server components |
| `StartClient` / `StopClient` | `Empty` → `Empty` | Start/stop the client |
| `UploadClientConfig` | `UploadClientConfigRequest` → `Empty` | Import a client context bundle (bytes) in client mode |
| `ListUsers` | `Empty` → `ListUsersResponse` | All configured users with tunnel mappings |
| `CreateUser` | `CreateUserRequest` → `Empty` | Create a user (name + port mappings) |
| `DeleteUser` | `DeleteUserRequest` → `Empty` | Delete a user by name |
| `GetUserConfig` | `GetUserConfigRequest` → `UserConfigResponse` | The user's client context bundle (`.twctx` bytes, no passphrase) |

The CLI's built-in client wraps the subset it needs: `GetStatus`,
`TestRelay`, `ListUsers`, `DeleteUser`, `DestroyRelay`, and `GetUserConfig`;
every CLI command that can use the daemon falls back to local (on-disk)
operation when no daemon answers on `server.api_port`.
