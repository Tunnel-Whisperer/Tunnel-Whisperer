# Web Dashboard

The web dashboard provides a browser-based interface for managing all aspects of Tunnel Whisperer.

## Starting the Dashboard

```bash
tw dashboard [--port PORT]
```

Default port is `8080`. The dashboard also starts automatically when running `tw serve` if `server.dashboard_port` is configured.

## Mode Selection

On first launch, the dashboard prompts you to choose a mode:

- **Server** — manage relay, users, and server lifecycle
- **Client** — upload config and connect to the server

## Server Mode Dashboard

The main page shows three cards:

### Server Card

- **Status indicators**: SSH, Xray, and Tunnel health (up/down/error)
- **Start/Stop/Restart** buttons with real-time progress via SSE
- Settings link to the config page

### Relay Card

- Domain, IP, and provider information
- Link to relay management page (provision, test, destroy, SSH terminal)

### Clients Card

- Online user count with live status badges
- User list sorted by online status
- Link to user management page

### Console

Real-time log streaming at the bottom of the page. Logs are captured from the application's `slog` output and streamed via Server-Sent Events.

## Client Mode Dashboard

### Client Card

- **Upload form** — drag-and-drop or browse for a config zip (shown when no config is loaded)
- **Status indicators**: Xray and Tunnel health
- **Connect/Disconnect/Reconnect** buttons

### Tunnels Card

- List of configured port mappings (clickable to copy `localhost:port`)
- Config update form (upload new config zip when stopped)

## Config Page

Accessible from the settings icon on any card. All settings are editable from the web UI and are persisted to `config.yaml` immediately. A restart (server) or reconnect (client) is needed for the changes to take effect.

### General Settings

- **Log Level** — dropdown to select debug/info/warn/error
- **Proxy** — SOCKS5 or HTTP proxy URL field

### Xray Transport Settings

- **Relay Host** — domain or IP of the relay server
- **Relay Port** — HTTPS port on the relay (default 443)
- **Path** — WebSocket path used by Xray (default `/tw`)

### Server Settings (server mode only)

- **SSH Port** — embedded SSH server listen port
- **API Port** — gRPC API listen port
- **Dashboard Port** — web dashboard listen port
- **Relay SSH Port** — SSH port on the relay for reverse tunneling
- **Relay SSH User** — SSH user on the relay
- **Remote Port** — port exposed on relay for clients
- **Temp Xray Port** — port used for the temporary Xray tunnel during relay config updates (user creation/registration). Change this if port 59000 is already in use on your system.

### Client Settings (client mode only)

- **SSH User** — username for SSH authentication to the server
- **Server SSH Port** — server's SSH port on the relay

### Config File

- **config.yaml** — read-only view of the current configuration file, auto-refreshed after each save

Changes trigger a "Configuration has changed" notification with a Restart (server) or Reconnect (client) prompt.

## Relay Page

- Relay status and connection details
- **Test** button — runs a 3-step connectivity diagnostic
- **Provision/Destroy** — relay lifecycle management
- **SSH Terminal** — interactive terminal to the relay via WebSocket + xterm.js

### SSH Terminal

The SSH terminal connects through a WebSocket to a Go SSH bridge that tunnels through Xray to the relay. Features:

- Full PTY with xterm-256color support
- Auto-resize on window/container resize
- Connect/Disconnect controls

## Users Page

- Sortable user list with online status, registration status, and tunnel count
- **Config outdated** badge (yellow) when a user's mappings were changed but they haven't re-downloaded their config
- Search and pagination
- **Create User** — form-based user creation with optional application template pre-fill
- **Duplicate** — create a new user with the same port mappings as an existing user (from user detail page)
- **Edit Mappings** — modify a user's port mappings, with option to add from an application template
- **Apply/Unregister** — batch operations for relay registration
- **Download** — export user config as zip (clears the config outdated flag)
- **Delete** — remove user and revoke access

## Apps Page

- List of application templates with name, mapping count, and port mapping preview
- **Create Application** — define a named set of port mappings
- **Edit** — modify an application template's name and mappings
- **Delete** — remove an application template

Application templates are reusable port mapping bundles. When creating or editing a user, select an application template to auto-fill port mappings instead of entering them manually.

!!! note "No retroactive changes"
    Editing an application template does not affect users that were previously created using it.

## Running as a System Service

The dashboard can run as a system service for unattended operation. The service starts `tw dashboard`, which auto-starts the server or client based on config mode.

=== "Linux"

    ```bash
    sudo tw service install   # create and enable systemd unit
    sudo tw service start     # start the service
    ```

=== "Windows"

    ```powershell
    tw.exe service install    # register with Windows SCM
    tw.exe service start      # start the service
    ```

The service restarts automatically on failure. Use `tw service stop` and `tw service uninstall` to stop and remove it.

See [Installation — Install as a System Service](../getting-started/installation.md#install-as-a-system-service) for setup details.

## Progress Events

Long-running operations (provisioning, starting, stopping) show real-time step-by-step progress via Server-Sent Events. Each step displays its status (running/completed/failed) with descriptive labels and error messages.
