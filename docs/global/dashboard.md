# Web Dashboard

The web dashboard provides a browser-based interface for managing Tunnel Whisperer. Its pages adapt to the machine's role — relay, server, or client.

## Starting the Dashboard

```bash
tw dashboard [--port PORT] [--listen ADDR]
```

Default port is `8080`. The dashboard is also served by the running server daemon (`tw server start`) when `server.dashboard_port` is configured, and by the [system service](status-service.md#tw-service-run-as-a-system-service). On launch it auto-starts the server (if the relay is provisioned) or auto-connects the client (if a relay is configured).

The dashboard binds `127.0.0.1` by default — it is only reachable from the machine it runs on, and the recommended way to use it remotely is an SSH port-forward (`ssh -L 8080:127.0.0.1:8080 <host>`), which keeps it on loopback and adds SSH's transport encryption.

## Signing in

The dashboard requires a **token** — there is no anonymous access. Fetch the current token from the same machine and paste it into the login page:

```bash
tw dashboard token            # prints the token
tw dashboard token --rotate   # generates a new token and invalidates all active sessions
```

The token lives in a `0600` file in the config directory (`dashboard.token`), readable only by the operator. A successful login sets a `SameSite=Strict`, `HttpOnly` session cookie (12-hour lifetime); rotating the token logs every session out. Automation can skip the login page by sending the token directly:

```bash
curl -H "Authorization: Bearer $(tw dashboard token)" http://127.0.0.1:8080/api/status
```

The same token also gates `/metrics` (Prometheus) so scrape jobs must present it.

### Exposing it off-loopback

Binding to a non-loopback interface serves the login token and session cookie over **cleartext HTTP**, so the dashboard **refuses** an off-loopback bind unless you explicitly acknowledge the risk:

```bash
tw dashboard --listen 0.0.0.0          # refused unless dashboard_allow_lan is set
```

To allow it, set `server.dashboard_allow_lan: true` in `config.yaml` (alongside `server.dashboard_listen`). Even then the dashboard prints a prominent warning: put a **TLS terminator** in front of it and restrict access to a trusted network. When it is reached over TLS (directly or via a terminating proxy that sets `X-Forwarded-Proto: https`), the session cookie is additionally marked `Secure`.

!!! warning "Cleartext off-loopback exposes the token"
    Over plain HTTP on a shared network, a passive observer can capture the bearer token or session cookie and take over the node. Prefer the loopback default with an SSH port-forward; only bind off-loopback behind TLS on a network you trust.

## Role Selection

On first launch — before a mode is set — the dashboard shows three cards to choose from:

- **Server** — run behind a firewall; connect to a relay, start SSH + Xray, let clients in
- **Client** — connect to an existing server through a relay
- **Relay** — own and manage the relay: provision it, hold the admin bundle, admit servers

Once a role is chosen (here or via the CLI), it is fixed and the navigation shows only that role's pages.

## Navigation

The nav bar shows the pages for the current role, plus the **current context name** and a **mode badge** on the right:

| Role | Pages |
| ---- | ----- |
| server | Status · Relay · Users · Apps · Stats · Config |
| relay | Status (relay home) · Relay · Servers · Config |
| client | Status · Config |

## Server Mode

### Status Page

- **Server card** — SSH, Xray, and Tunnel health; **Start/Stop/Restart** with real-time progress via SSE
- **Relay card** — domain, IP, provider; link to the relay management page
- **Clients card** — online user count with live status badges; user list sorted by online status
- **Bandwidth card** — when analytics is enabled: top 3 users by total traffic, active connection counts, link to the full **Stats** page
- **Console** — real-time log streaming (the application's `slog` output over Server-Sent Events)

### Users Page

- Sortable user list with online status, registration status, and tunnel count
- **Search box and pagination** (client-side filtering)
- **Enrollment happens in the CLI** — `tw server user invite <name> -m <port:port>` mints the code and does the client's key/cert exchange in one step; the dashboard manages the resulting users
- **Apply/Unregister** — batch relay registration
- **Delete** — remove the user and revoke access

### Apps Page

Application templates are reusable port-mapping bundles: list, **Create**, **Edit**, **Delete**. When creating or editing a user, select a template to auto-fill mappings.

!!! note "No retroactive changes"
    Editing an application template does not affect users that were previously created from it.

### Stats Page

Full sortable, searchable table of all tunnel bandwidth data with live polling: filter by user, sort by port/sent/received/connections, 10 rows per page, live active-connection badge.

## Relay Mode

### Status Page (Relay Home)

The relay owner's landing page: a relay summary card (domain, instance, IP, provider — or a **Provision Relay** button if none exists yet) and a reminder that the **admin bundle** is the relay's only backup: it holds the CA keypair and relay SSH key, carries no passphrase, and has no recovery if lost.

### Servers Page

The multi-tenant view — every server enrolled on this relay, in a **live table queried from the relay** (Server ID, Path, Port, Enrolled, Tunnel up/down):

- **Search box** and **tunnel up/down filter** narrow the table client-side; a Refresh button re-queries the relay
- **Un-enroll** — per-row action with a confirmation; the server's relay access and all its live connections end immediately

!!! note "Enrollment is CLI-only"
    Admitting a new server or client tenant (`tw relay invite`, `tw server user invite`) requires a spoken SAS confirmation and isn't yet exposed in the dashboard — run it from the CLI, then manage the result here.

## Client Mode

### Status Page

- **Client card** — upload form for the context bundle (when none is loaded), Xray and Tunnel health, **Connect/Disconnect/Reconnect**
- **Tunnels card** — port mappings with the *effective* local port (click to copy `localhost:port`). Ports remapped on this machine show an `override` badge; **edit** changes a tunnel's local port and **reset** restores the admin default (both take effect on the next reconnect). Bundle update form when stopped.
- **Bandwidth card** — when analytics is enabled: per-port sent/received bytes and active connections

## Relay Management Page

Available in relay and server mode under **Relay**:

- Relay status and connection details (copyable IP)
- **Test Connectivity** — runs the multi-step relay diagnostic
- **Provision/Destroy** — relay lifecycle with step-by-step progress
- **Close Port 22** — shown when the relay was provisioned with public SSH open; closes it so SSH is reachable only through the tunnel again
- **SSH Terminal** — interactive terminal to the relay via WebSocket + xterm.js

### SSH Terminal

The terminal connects through a WebSocket to a Go SSH bridge that tunnels through Xray to the relay:

- Full PTY with xterm-256color support
- Auto-resize on window/container resize
- Connect/Disconnect controls
- When the relay's port 22 is public, a connection-mode selector offers the Xray tunnel or a direct connection

## Config Page

All settings are editable from the web UI and persisted to `config.yaml` immediately. A restart (server) or reconnect (client) applies them — a "Configuration has changed" notification prompts for it.

### Contexts Card

The stored [contexts](contexts.md) as a table with a **search box and role filter**. Switching a context re-seals the current profile, activates the selected one, and reloads the page — the nav badge follows.

### Settings

- **Log Level** and **Proxy** — separate cards for log verbosity and the outbound [proxy](proxy.md) URL
- **Xray Transport** — relay host, relay port (default 443), path
- **Server** (server mode) — SSH port, API port, dashboard port, relay SSH port/user, remote port, temp Xray port
- **Client** (client mode) — SSH user, server SSH port, local Xray port, listen address (`127.0.0.1` default; `0.0.0.0` to expose tunnels on all interfaces, e.g. in a container)
- **Analytics** — enable bandwidth statistics (takes effect immediately), history size
- **config.yaml** — read-only view of the live file, refreshed after each save

## Live Updates

Long-running operations (provisioning, starting, stopping, enrolling) stream real-time step-by-step progress over **Server-Sent Events**, each step labeled running/completed/failed. Status indicators, online-user badges, and the log console update live the same way — no manual refresh needed.

## Running as a System Service

The dashboard is what the `tw` system service runs. See [Status, Service & Completion](status-service.md#tw-service-run-as-a-system-service):

```bash
sudo tw service install
sudo tw service start
```
