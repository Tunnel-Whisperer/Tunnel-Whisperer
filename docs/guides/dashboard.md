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

Accessible from the settings icon on any card:

- **Log Level** — dropdown to select debug/info/warn/error, saved to config
- **Proxy** — SOCKS5 or HTTP proxy URL field
- **config.yaml** — read-only view of the current configuration file

Changes to log level or proxy trigger a "Configuration has changed" notification with a Restart (server) or Reconnect (client) prompt.

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
- Search and pagination
- **Create User** — form-based user creation
- **Apply/Unregister** — batch operations for relay registration
- **Download** — export user config as zip
- **Delete** — remove user and revoke access

## Progress Events

Long-running operations (provisioning, starting, stopping) show real-time step-by-step progress via Server-Sent Events. Each step displays its status (running/completed/failed) with descriptive labels and error messages.
