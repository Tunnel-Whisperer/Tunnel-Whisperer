# Global

One binary, three roles. Everything on this page — and in this section — applies to **every** `tw` installation, no matter which role it plays.

## One Binary, Three Roles

Every machine runs the same `tw` binary. What it *does* is decided by the **mode** stored in its active context:

| Role | Who runs it | Top-level commands |
| ---- | ----------- | ------------------ |
| **Relay** | The relay admin who owns the relay VM: provisions it, holds the CA, admits servers | `tw relay create / destroy / test / ssh / status / invite / get-servers / un-enroll-server` |
| **Server** | The server operator, who runs the network being exposed: joins a relay, manages users and apps, runs the tunnel endpoint | `tw server start / test / status / user … / app …` |
| **Client** | The client user: joins via invite or imports a bundle, brings up local port forwards | `tw client connect / listen / test / status` |

The mode is set by the first role action a machine performs — `tw relay create` (relay), `tw join <relay-host> <code>` against a `tw relay invite` code (server or client — the issuer's invite decides which), or importing **and activating** a context bundle (`tw config import --activate`) — and every context carries exactly one role.

!!! warning "One role per context"
    Once a context has a role, it keeps it — commands of the other roles refuse to run in it:

    ```text
    Error: this command requires relay mode, but tw is configured in server mode
    ```

    To act in a different role on the same machine, use a separate [context](contexts.md).

!!! note "The mode is not the security boundary"
    The `mode` field only steers which commands run locally. The real access control lives on the other end of the wire: the relay routes every tunnel via **mTLS** (Caddy verifies a CA-issued client certificate and matches it to a tenant before proxying), and the relay's and server's SSH `authorized_keys` gate every tenant and user with per-port restrictions. Editing the local mode gains nothing — the relay and server still reject unknown keys and certificates.

## Shared Across All Roles

These commands and facilities work identically in every role:

| Feature | Command | Guide |
| ------- | ------- | ----- |
| Contexts — store, switch, export, and import complete identities | `tw config …` | [Contexts](contexts.md) |
| Unified status — context, mode, relay, live component health | `tw status` | [Status, Service & Completion](status-service.md) |
| System service — run on boot via systemd / Windows SCM / launchd | `tw service …` | [Status, Service & Completion](status-service.md) |
| Outbound proxy — route all tw traffic (tunnel, enrollment, relay ops) through SOCKS5/HTTP | `tw proxy …` | [Proxy](proxy.md) |
| Web dashboard — browser UI on :8080, pages adapt to the role | `tw dashboard` | [Dashboard](dashboard.md) |
| Shell completion — zsh, with dynamic candidates | `tw completion` | [Status, Service & Completion](status-service.md#shell-completion) |

## Global Flags

Every command accepts these persistent flags:

```text
--log-level string    log level (debug, info, warn, error) — persisted to config
--log-format string   log format (text, json) — persisted to config
--config-dir string   config/state directory to use instead of the system default;
                      flag form of TW_CONFIG_DIR (no permissions needed)
```

## Config Directory

All state — `config.yaml`, keys, certificates, users, contexts — lives in one directory:

| Platform | Path |
| -------- | ---- |
| Linux / macOS | `/etc/tw/config/` |
| Windows | `C:\ProgramData\tw\config\` |

Override it with the `TW_CONFIG_DIR` environment variable or the `--config-dir` flag — handy for running without root or keeping several independent installations side by side.
