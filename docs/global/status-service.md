# Status, Service & Completion

## `tw status`

The top-level status command is **ungated** — it works in any role, and even on a machine that isn't set up yet. It answers "what is going on here?" in one screen.

```bash
tw status
```

Every status report starts with the **unified context header**:

```text
  Context:  alice (81d0b44f) — 3 stored
  Mode:     client
  User:     alice
  Relay:    relay.example.com
  Config:   /etc/tw/config/config.yaml
```

- **Context** — active context name, short ID, and how many contexts are stored.
- **Mode** — `relay`, `server`, or `client` (`—` if not set up).
- **User / Relay** — shown when the context has them.
- **Config** — the config file actually in use (reflects `TW_CONFIG_DIR` / `--config-dir`).

Below the header comes mode-appropriate live status, written for humans — components read `working` / `not working`, the relay reads `Provisioned: yes` / `no`:

```text
  Users:  4 (2 connected)

  Relay:
    Provisioned: yes
    IP:          203.0.113.7
    Provider:    Hetzner

  Server:
    State:   running
    SSH:     working
    Xray:    working
    Tunnel:  working
```

- On **servers**, the user line includes the **live connected user count**.
- On **clients**, a `Client:` block shows state, Xray, and tunnel health instead.
- If no daemon is running, `tw status` reports local state and points you at the fix: `(daemon not running — start with 'tw server start' or 'tw dashboard')`.
- If a running service's mode or relay differs from the active context (you switched contexts but didn't restart), a warning spells out the drift and how to resolve it.

Each role also has a gated variant with the same output: `tw relay status`, `tw server status`, `tw client status`.

## `tw service` — Run as a System Service

`tw service` registers the binary with the native service manager so it starts on boot and runs unattended. The service runs `tw dashboard`, which auto-starts the server or client according to the config mode.

```bash
tw service install      # register the service (name: tw, display: Tunnel Whisperer)
tw service start
tw service stop
tw service uninstall
```

=== "Linux (systemd)"

    ```bash
    sudo tw service install
    sudo tw service start
    sudo systemctl status tw    # check
    sudo journalctl -u tw -f    # follow logs
    ```

=== "Windows (SCM)"

    ```powershell
    # elevated PowerShell
    tw.exe service install
    tw.exe service start
    ```

    Also manageable from `services.msc` (service name `tw`, display name **Tunnel Whisperer**). Logs go to a file in the config directory, since the SCM discards console output.

=== "macOS (launchd)"

    ```bash
    sudo tw service install
    sudo tw service start
    ```

    Creates a LaunchDaemon that keeps the service running and starts it on boot.

The service restarts automatically on failure. See [Installation](../getting-started/installation.md#install-as-a-system-service) for binary placement and platform details.

!!! note "Context switches need a service restart"
    The service loads its context at startup. After `tw config use-context`, restart the service to apply the switch — `tw status` warns as long as they disagree.

## Shell Completion

`tw completion` generates a **zsh** completion script:

```bash
# current session
source <(tw completion)

# every session — add to ~/.zshrc
source <(tw completion)

# or install into the zsh completions directory
tw completion > "${fpath[1]}/_tw"
```

Completion is dynamic, not just static subcommands. From local state only (never the relay), it offers:

- **context names and short IDs** for `tw config use-context / delete-context / export / rename-context` — annotated with role, user, and relay;
- **usernames** for `tw server user edit / delete / apply / unregister` and `tw config export-user` — annotated with tunnel count and applied state;
- **server IDs** for `tw relay un-enroll-server` — annotated with port and enrollment date;
- **application names** for `tw server app edit / delete`.
