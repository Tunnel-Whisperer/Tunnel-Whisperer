# Running the Server

Once the server has [joined a relay](join-relay.md), a single command brings everything up.

## Start

```bash
tw server start
```

This runs in the foreground (Ctrl-C stops it) and starts, in one process:

1. **Embedded SSH server** on `:2222` — the tunnel endpoint clients land on. It enforces per-user `permitopen` restrictions from `authorized_keys`, which it re-reads on every auth attempt.
2. **Xray client tunnel** to the relay — VLESS + XHTTP over TLS on port 443, presenting the server's client certificate to the relay's mutual-TLS gate.
3. **SSH reverse tunnel** through Xray — publishes the server's SSH endpoint on the relay's `127.0.0.1:<remote_port>` (the port assigned at enrollment), so clients can reach it. End-to-end SSH encryption means the relay never sees plaintext.
4. **gRPC API** on `:50051` — the local management API. Other `tw` commands (`status`, `user list`, `config export-user`, ...) talk to the running daemon through it, and fall back to operating on local files when it's not running.
5. **Web dashboard** on `http://localhost:8080` — live status, user management, logs. Started when `server.dashboard_port` is set (it is by default); set it to `0` to disable.

All ports are configurable in `config.yaml` under the `server` section (`ssh_port`, `api_port`, `dashboard_port`).

## Test

```bash
tw server test
```

Verifies the whole path to the relay — DNS, HTTPS/mTLS admission, and SSH over the tunnel. Expect "tunnel and shell working". Runs via the daemon when the server is up, or standalone when it isn't.

## Status

```bash
tw server status
```

Prints the unified status view: the active context (name, mode, relay, config path), user counts (total and currently connected), the relay block (provisioned, IP, provider), and the health of each server component (SSH, Xray, tunnel) with any tunnel error.

If the daemon isn't running, status falls back to local state and says so. If a running service was started under a *different* context than the currently active one (after a `tw config use-context` switch), status prints an explicit mismatch warning — restart the service to apply the switch.

!!! tip "Top-level `tw status`"
    Plain `tw status` works in any mode (or none) and shows the same view — it's the "what is going on here?" entry point.

## Run as a Service

To keep the server running in the background and start it on boot:

=== "Linux"

    ```bash
    sudo tw service install
    sudo tw service start
    ```

=== "Windows"

    ```powershell
    tw.exe service install
    tw.exe service start
    ```

`tw service stop` and `tw service uninstall` undo these. While the service is running, the regular CLI commands transparently talk to it over the gRPC API.

## What's Next

- [Create users](users.md) for your clients
- [Configure a proxy](../global/proxy.md) if the server sits behind a corporate proxy
- [Troubleshooting](../guides/troubleshooting.md) if the tunnel won't come up
