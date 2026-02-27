# CLI Commands

All interaction with Tunnel Whisperer happens through the `tw` binary.
Commands are mode-aware: server-only commands fail with an error when the
config is set to `client`, and vice versa.

## Command reference

| Command | Mode | Description |
|---|---|---|
| `tw serve` | server | Start the Tunnel Whisperer server (SSH, Xray, reverse tunnel, dashboard, gRPC API) |
| `tw connect` | client | Connect to a relay as a client and establish local port forwards |
| `tw dashboard` | any | Start the web dashboard with auto-start logic for server or client |
| `tw status` | any | Show current server/client status (connects to daemon via gRPC, falls back to local) |
| `tw create relay-server` | server | Interactively provision a relay server on a cloud provider |
| `tw create user` | server | Create a client user with tunnel access (interactive port mapping) |
| `tw list users` | server | List all configured users and their tunnel mappings |
| `tw delete user <name>` | server | Delete a user (with confirmation prompt) |
| `tw export user <name>` | server | Export a user's config bundle as a `.zip` file |
| `tw test relay` | any | Test connectivity to the relay server (DNS, HTTPS, WebSocket, SSH) |
| `tw relay ssh` | server | Open an interactive SSH shell on the relay server |
| `tw destroy relay-server` | server | Destroy the provisioned relay server via Terraform |
| `tw proxy` | any | Show the current outbound proxy setting |
| `tw proxy set <url>` | any | Set the outbound proxy URL |
| `tw proxy clear` | any | Remove the outbound proxy |
| `tw completion` | any | Generate a zsh completion script |

## Global flags

| Flag | Values | Default | Description |
|---|---|---|---|
| `--log-level` | `debug`, `info`, `warn`, `error` | `info` | Set the log verbosity level |

The `--log-level` flag is **persisted to the config file** when specified
explicitly. On subsequent runs without the flag, the saved value is used
automatically.

```bash
# Set log level for this run and persist it
tw serve --log-level debug

# Later runs use the persisted level without needing the flag
tw serve
```

## Mode enforcement

Tunnel Whisperer enforces a strict separation between server and client
commands. The `mode` field in `config.yaml` determines which commands are
available.

!!! warning "Mode mismatch error"
    Running a server command while configured in client mode (or vice versa)
    produces an error:

    ```
    Error: this is a server command, but tw is configured in client mode
    ```

If `mode` is empty (not yet configured), all commands are allowed.

## Dashboard auto-start

When launched with `tw dashboard`, the daemon automatically starts the
appropriate service:

- **Server mode** -- if the relay is provisioned, the server starts automatically.
- **Client mode** -- if `xray.relay_host` is set, the client connects automatically.

The dashboard also starts the gRPC API, so CLI commands like `tw status` and
`tw list users` can communicate with the running daemon.

## Shell completion

Generate and install zsh completions:

```bash
# Load in current session
source <(tw completion)

# Persist across sessions (add to ~/.zshrc)
source <(tw completion)

# Or write to the zsh completions directory
tw completion > "${fpath[1]}/_tw"
```
