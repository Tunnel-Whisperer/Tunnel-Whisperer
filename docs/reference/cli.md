# CLI Commands

All interaction with Tunnel Whisperer happens through the `tw` binary. The
command tree is organized by **role**: `tw relay …` for the relay owner,
`tw server …` for a server operator, `tw client …` for a client, plus
role-neutral groups (`config`, `proxy`, `service`, `status`, `dashboard`,
`completion`). Commands are mode-aware: a command gated to one role fails with
an error when the active profile is configured for another (see
[Mode enforcement](#mode-enforcement)).

## Global flags

Available on every command:

| Flag | Values | Default | Description |
|---|---|---|---|
| `--log-level` | `debug`, `info`, `warn`, `error` | `info` | Log verbosity. Persisted to the config file when set explicitly. |
| `--log-format` | `text`, `json` | `text` | Log output format. `json` maps attributes to OpenTelemetry semantic-convention names. Persisted when set explicitly. |
| `--config-dir` | path | _(system default)_ | Config/state directory to use instead of the system default. Flag form of the `TW_CONFIG_DIR` environment variable (the flag wins over an inherited env value). Lets you run without root. |
| `--version` | | | Print the version and exit. |

`--log-level` and `--log-format` are **persisted to the config file** when
specified explicitly; later runs without the flag reuse the saved value:

```bash
# Set log level for this run and persist it
tw server start --log-level debug

# Later runs use the persisted level without needing the flag
tw server start
```

## Top-level commands

| Command | Description |
|---|---|
| [`tw relay`](#tw-relay-relay-role) | Manage the relay server (relay role): provision, destroy, enroll servers, SSH in |
| [`tw server`](#tw-server-server-role) | Server-mode commands: run the server, join a relay, manage users and app templates |
| [`tw client`](#tw-client-client-role) | Client-mode commands: connect, listen address |
| [`tw config`](#tw-config-contexts) | Manage relay contexts (switch between relays/identities), import/export bundles |
| [`tw status`](#tw-status) | Overall status: active context, mode, and its live status (ungated — works on any machine) |
| [`tw dashboard`](#tw-dashboard) | Start the web dashboard (with server/client auto-start) |
| [`tw proxy`](#tw-proxy) | Show or configure the outbound proxy |
| [`tw service`](#running-as-a-service) | Manage the system service (Linux systemd / Windows SCM / macOS launchd) |
| [`tw completion`](#shell-completion) | Generate the zsh completion script |

## `tw relay` (relay role)

Everything the relay owner does to the relay. All subcommands require
`mode: relay`.

| Command | Description |
|---|---|
| `tw relay create` | Provision a relay server — interactive wizard (cloud providers via Terraform, or a manual bring-your-own-VM flow). Writes the relay's portable context bundle (domain sanitized: `tw_relay-example-com.twctx`) on success. |
| `tw relay destroy` | Destroy the provisioned relay (Terraform for cloud relays; prompts for AWS credentials when needed). |
| `tw relay enroll-server <join-request.json>` | Enroll a joining server onto the relay: registers it, allocates its port, rewrites the relay's Caddyfile/Xray config/`authorized_keys`, and writes a `tw_join_response_<server-id>.json` file to send back. |
| `tw relay get-servers` | List servers registered on the relay (`SERVER-ID`, `PATH`, `PORT`, `ENROLLED`, `TUNNEL` up/down — live-checked against the relay). |
| `tw relay un-enroll-server <server-id>` | Un-enroll a server from the relay and kill its live connections. Prints the server's details, then asks for confirmation. |
| `tw relay ssh` | Open an interactive SSH shell on the relay server (through the tunnel). |
| `tw relay status` | Relay-mode variant of [`tw status`](#tw-status). |
| `tw relay test` | Test connectivity to the relay server. |

### `tw relay create` flags

| Flag | Description |
|---|---|
| `--provider <name>` | Provider selection; currently only `manual` is accepted as a flag value. Combined with `--domain` and `--ip`, the manual flow runs fully non-interactively. Cloud providers (Hetzner, DigitalOcean, AWS) are chosen interactively in the wizard. |
| `--domain <domain>` | Relay domain (skips the domain prompt). |
| `--ip <addr>` | Relay public IP (manual provider only; skips the IP prompt). Requires `--provider manual`. |
| `--ssh-open` | Open the relay's SSH port (22) to the internet. Default: SSH is reachable only through the tunnel. See the note below. |

!!! note "`--ssh-open`"
    tw reaches the relay only through the encrypted tunnel; by default its
    management key is pinned `from="127.0.0.1"` and never works over public
    port 22. `--ssh-open` opens port 22 in the relay firewall and leaves the
    tw key unpinned so it also works over the open port. This is meant for
    your own maintenance access; without the flag the firewall allows only
    ports 80 and 443 and `sshd` listens on localhost only.

The manual flow generates an install script (`tw-install-<domain>.sh`, printed
and saved to the current directory) that you run as root on your own VM — no
Terraform, no cloud credentials. The Terraform flow requires `terraform` in
`PATH` and prompts for provider credentials.

### `tw relay un-enroll-server` flags

| Flag | Description |
|---|---|
| `--yes` | Skip the confirmation prompt (the server details are still printed so scripted runs log what was removed). |

## `tw server` (server role)

All subcommands require `mode: server`.

| Command | Description |
|---|---|
| `tw server start` | Start the Tunnel Whisperer server: embedded SSH server, Xray, reverse tunnel, plus the dashboard (if `server.dashboard_port` > 0) and the gRPC API. |
| `tw server join-relay <relay-host>` | Generate a join request (`tw_join_<server-id>.json`) to send to the relay admin. |
| `tw server join-relay --apply <response.json>` | Apply the admin's join response: records the relay host, path, and allocated port. |
| `tw server status` | Server-mode variant of [`tw status`](#tw-status). |
| `tw server test` | Test connectivity to the relay server. |
| `tw server user …` | Manage client users (below). |
| `tw server app …` | Manage application templates (below). |

### `tw server join-relay` flags

| Flag | Description |
|---|---|
| `--apply <file>` | Apply an admin join-response file instead of generating a request. |
| `--new-context <name>` | First create and switch to a fresh context of this name (preserving the current one), then join in it. |

The join handshake is file-based:

```bash
# On the server:
tw server join-relay relay.example.com     # writes tw_join_<server-id>.json
# Send the file to the relay admin, who runs:
tw relay enroll-server tw_join_<server-id>.json
# The admin sends back tw_join_response_<server-id>.json; on the server:
tw server join-relay --apply tw_join_response_<server-id>.json
tw server start
```

### `tw server user`

| Command | Description |
|---|---|
| `tw server user create [name]` | Create a client user with tunnel access. With a name argument it runs non-interactively from flags; without one it prompts. Supports `--single-session` to enforce one concurrent connection per user. |
| `tw server user list` | List all configured users and their tunnel mappings. |
| `tw server user edit <name>` | Edit a user's port mappings (interactive). |
| `tw server user delete <name>` | Delete a user (with confirmation prompt). |
| `tw server user apply [name...]` | Register users on the relay (all users if no names are given). |
| `tw server user unregister <name>` | Unregister a user from the relay (revoke tunnel access without deleting the user). |
| `tw server user single-session <name> [on\|off]` | Show or set single-session enforcement (one concurrent SSH connection per user). No argument shows the current state; `on` or `off` sets it. Rewrites the user's authorized_keys entry; takes effect on the next auth attempt. |

`tw server user create` flags:

| Flag | Description |
|---|---|
| `-m`, `--map <clientPort:serverPort>` | Port mapping (repeatable), e.g. `-m 8080:80`. |
| `--from <user>` | Copy port mappings from an existing user (mutually exclusive with `--map`). |
| `--single-session` | Enforce one concurrent SSH connection per user; subsequent login attempts while one is active are rejected. Takes effect on the next auth attempt. |

```bash
tw server user create alice -m 8080:80 -m 5432:5432
tw server user create bob --from alice
```

To hand the user their credentials, export them as a client context bundle
with [`tw config export-user`](#tw-config-contexts).

### `tw server app`

Application templates are named, reusable bundles of port mappings.

| Command | Description |
|---|---|
| `tw server app list` | List all application templates. |
| `tw server app create` | Create an application template (interactive). |
| `tw server app edit <name>` | Edit an application template (interactive). |
| `tw server app delete <name>` | Delete an application template (with confirmation). |

## `tw client` (client role)

All subcommands require `mode: client`.

| Command | Description |
|---|---|
| `tw client connect` | Connect to the relay: starts the embedded Xray client and the SSH forward tunnels. Runs until Ctrl-C. Supports repeatable one-shot overrides: `--map <local_port>:<server_port>` (ssh -L ordering, not persisted). |
| `tw client listen [address]` | Show or set the local interface forwarded tunnels bind to. No argument prints the current address (default `127.0.0.1`); pass an IP to change it (`0.0.0.0` to expose tunnels on all interfaces, e.g. in a container). Takes effect on next reconnect. |
| `tw client set-port [server_port] [local_port]` | Override the local port a tunnel binds on this machine, keyed by its server port. No arguments lists tunnels with default, override, and effective ports; `--clear` removes an override. Takes effect on next reconnect. |
| `tw client status` | Client-mode variant of [`tw status`](#tw-status). |
| `tw client test` | Test connectivity to the relay server. |

## `tw config` (contexts)

Contexts are kubectl-style stored profiles — each one a complete relay/server/
client identity (config, keys, certs). Switching contexts seals the current
profile to disk and unseals the target. These commands work in any mode.

| Command | Description |
|---|---|
| `tw config get-contexts` | List stored contexts (`CURRENT`, `NAME`, `ID`, `ROLE`, `USER`, `RELAY`). |
| `tw config current-context` | Print the active context name. |
| `tw config use-context <name\|id>` | Switch the active context (seals the current one, reconnects). Warns if a running `tw` service is still serving the old context. |
| `tw config new-context <name>` | Create a fresh empty context and switch to it (the current one is preserved). |
| `tw config rename-context <old-name\|id> <new>` | Rename a context. |
| `tw config delete-context <name\|id>` | Delete a stored context. Deleting the only, active context is a **full reset** (removes all tw configuration from the machine; confirmed interactively, refused while the service is running). |
| `tw config import <bundle.twctx>` | Import a bundle as a new context. Prompts before replacing an existing context of the same name. |
| `tw config export [name\|id]` | Export a context as a portable bundle (`tw_<name>.twctx`). No argument exports the active context. |
| `tw config export-user <name>` | **Server only.** Package one of this server's users as a `role: client` context bundle (`<name>-tw-context.twctx`). The client imports it with `tw config import <file> --activate`. |
| `tw config view` | Print the active config file (path header + raw YAML). `--as-json` prints it as indented JSON instead (no path header). |

### `tw config import` flags

| Flag | Description |
|---|---|
| `--name <name>` | Context name (default: derived from the bundle, e.g. the relay domain or user name). |
| `--activate` | Switch to the imported context immediately (applies its mode). |
| `--force` | Replace an existing context of the same name without prompting. |

!!! warning "Bundles carry no passphrase"
    All context bundles (`.twctx`) are passphrase-less. A bundle is the
    portable identity for its relay/context — as sensitive as the keys inside
    it. Transfer it over a trusted channel.

## `tw status`

`tw status` is **ungated** — it works on any machine, set up or not. It
prints an identity header (active context name/ID, mode, user, relay, config
file path), then the mode's live status: user counts, relay provisioning
(with IP), and server/client component health (SSH, Xray, tunnel) when a
daemon is running. Without a daemon it reports what it can from disk and
tells you how to start one.

The role-scoped variants (`tw relay status`, `tw server status`,
`tw client status`) show the same view but are gated to their mode.

If a running `tw` service's mode or relay differs from the active on-disk
context (e.g. after `tw config use-context`), status prints a mismatch
warning telling you to restart the service.

## `tw dashboard`

Starts the web dashboard (default port 8080, from `server.dashboard_port`)
and the gRPC API, then auto-starts the appropriate role:

- **Server mode** — if the relay is provisioned, the server starts automatically.
- **Client mode** — if `xray.relay_host` is set, the client connects automatically.

| Flag | Description |
|---|---|
| `--port <n>` | Dashboard listen port (overrides config). |
| `--listen <addr>` | Dashboard listen address (default `127.0.0.1`, loopback only; set to `0.0.0.0` to expose on all interfaces). Overrides config. |

The dashboard also starts the gRPC API, so CLI commands like `tw status` and
`tw server user list` talk to the running daemon instead of reading state
from disk.

## `tw proxy`

Configure the outbound proxy used for all outbound connections. Works in all
three modes.

| Command | Description |
|---|---|
| `tw proxy` | Show the current proxy. |
| `tw proxy set <url>` | Set the proxy URL. Takes effect on next server/client start. |
| `tw proxy clear` | Remove the proxy. |

Supported URL formats: `socks5://host:port`, `socks5://user:pass@host:port`,
`http://host:port`, `http://user:pass@host:port`.

## Mode enforcement

The `mode` field in `config.yaml` (`relay`, `server`, or `client`; the legacy
value `admin` is read as `relay`) determines which commands are available.
Running a command outside its allowed mode produces:

```
Error: this command requires server mode, but tw is configured in client mode
```

If `mode` is empty (not yet configured), all commands are allowed.

The mode field is **tamper-evident**: profiles carry a `mode_auth` signature
over the mode and the profile's identity key. A present-but-invalid signature
is refused with instructions to re-enroll/re-import/re-create; a missing
signature on legacy profiles produces a one-time warning (a relay self-heals
by re-signing). This is tamper-evidence only — the real role boundary is the
relay's `authorized_keys` and the mTLS trust chain (see
[Security](../security/index.md)).

## Running as a Service

Tunnel Whisperer can run as a system service on Linux (systemd), Windows (SCM), and macOS (launchd). The
service runs `tw dashboard`, which auto-starts the server or client based on
the config mode.

=== "Linux (systemd)"

    ```bash
    sudo tw service install    # writes /etc/systemd/system/tw.service, enables it
    sudo tw service start      # systemctl start tw
    sudo tw service stop       # systemctl stop tw
    sudo tw service uninstall  # stops, disables, removes the unit file
    ```

=== "Windows (SCM)"

    ```powershell
    tw.exe service install    # registers service "tw" with the Service Control Manager
    tw.exe service start      # starts the service
    tw.exe service stop       # stops the service
    tw.exe service uninstall  # removes the service
    ```

## Shell completion

`tw completion` generates a **zsh** completion script:

```bash
# Load in current session
source <(tw completion)

# Persist across sessions (add to ~/.zshrc)
source <(tw completion)

# Or write to the zsh completions directory
tw completion > "${fpath[1]}/_tw"
```

Completion is **dynamic** for arguments that name local state — context names
and IDs (`tw config use-context/delete-context/export/rename-context`), user
names (`tw server user edit/delete/unregister/apply`, `tw config
export-user`), enrolled server IDs (`tw relay un-enroll-server`), and app
template names (`tw server app edit/delete`). Candidates come with
descriptions (role, relay, port count, applied state) and are read purely
from local files — completion never dials the relay or the daemon.
