# Global

One binary, three roles. Everything on this page — and in this section — applies to **every** `tw` installation, no matter which role it plays.

## One Binary, Three Roles

Every machine runs the same `tw` binary. What it *does* is decided by the **mode** stored in its active context:

| Role | Who runs it | Top-level commands |
| ---- | ----------- | ------------------ |
| **Relay** | The admin who owns the relay VM: provisions it, holds the CA, admits servers | `tw relay create / destroy / test / ssh / status / enroll-server / get-servers / un-enroll-server` |
| **Server** | The operator of the network being exposed: joins a relay, manages users and apps, runs the tunnel endpoint | `tw server start / join-relay / test / status / user … / app …` |
| **Client** | The person connecting in: imports a user bundle, brings up local port forwards | `tw client connect / listen / test / status` |

The mode is set by the first role action a machine performs — `tw relay create` (relay), `tw server join-relay <relay-host>` (server — the request step already stamps it), or importing **and activating** a user bundle (`tw config import --activate`, client) — and every context carries exactly one role.

!!! warning "Modes are permanent per machine (per context)"
    Once a context has a role, commands of the other roles refuse to run:

    ```text
    Error: this command requires relay mode, but tw is configured in server mode
    ```

    To act in a different role on the same machine, use a separate [context](contexts.md).

## Signed Mode (Tamper Evidence)

The `mode` field in `config.yaml` is **signed** (ed25519) against the profile's identity. If the file is hand-edited to flip a client into a server — or the signature no longer matches the identity — `tw` refuses to run role commands until the profile is restored (re-enroll for servers, re-import for clients, re-create for relays). Relay profiles hold their own signing key and self-heal an unsigned mode.

!!! note "Tamper evidence, not the security boundary"
    The signature only makes local tampering *evident*. The real access control lives on the other end of the wire: the relay admits tunnels via **mTLS** (Caddy verifies a CA-issued client certificate at the TLS handshake), and the server's SSH `authorized_keys` gates every user with per-port `permitopen` restrictions. A tampered local mode gains nothing — the relay and server still reject unknown keys and certificates.

## Shared Across All Roles

These commands and facilities work identically in every role:

| Feature | Command | Guide |
| ------- | ------- | ----- |
| Contexts — store, switch, export, and import complete identities | `tw config …` | [Contexts](contexts.md) |
| Unified status — context, mode, relay, live component health | `tw status` | [Status, Service & Completion](status-service.md) |
| System service — run on boot via systemd / Windows SCM / launchd | `tw service …` | [Status, Service & Completion](status-service.md) |
| Outbound proxy — route all traffic through SOCKS5/HTTP | `tw proxy …` | [Proxy](proxy.md) |
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
