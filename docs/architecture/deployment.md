# Deployment View

## Configuration

Default `config.yaml`:

```yaml
mode: ""                           # "relay", "server", or "client" (enforced by CLI)
log_level: info                    # debug, info, warn, error
log_format: text                   # text or json
proxy: ""                          # e.g. "socks5://user:pass@host:port" or "http://host:port"

xray:
  uuid: ""                         # auto-generated on first run
  relay_host: ""                   # e.g. relay.example.com
  relay_port: 443
  path: /tw                        # becomes /tw/<server-id> once provisioned/enrolled
  # client_cert_path/client_key_path are derived at runtime (mTLS client cert)

server:                            # only needed for `tw server start` / relay role
  ssh_port: 2222
  api_port: 50051
  dashboard_port: 8080
  relay_ssh_port: 22
  relay_ssh_user: ubuntu
  remote_port: 2222                # port published on the relay for this server's clients
  temp_xray_port: 59000            # temp management tunnel for relay config updates

client:                            # only needed for `tw client connect`
  ssh_user: tunnel
  server_ssh_port: 2222            # the server's remote_port on the relay
  xray_port: 54001                 # local dokodemo-door inbound
  listen_address: 127.0.0.1        # interface tunnels bind to (0.0.0.0 to expose)
  tunnels:
    - local_port: 5432             # listen on client localhost
      remote_host: 127.0.0.1      # target on server (localhost only)
      remote_port: 5432            # PostgreSQL

# mode_auth:                       # ed25519 signature over (mode, identity) —
#   sig: ...                       # written by relay create / enrollment / user
#   issuer: ...                    # export; makes the mode field tamper-evident
```

!!! note "Mode field"
    The `mode` field prevents accidental cross-role usage. When set, `requireMode()` in the CLI rejects commands belonging to other roles (legacy `admin` is canonicalized to `relay` on read). The mode is additionally ed25519-signed (`mode_auth`, see `internal/ops/modeauth`) as tamper-evidence: a present-but-invalid signature is refused, an unsigned mode is legacy-tolerated with a warning (the relay self-heals by re-signing with its own key). The dashboard adapts its UI based on this field.

---

## File Layout (Server / Relay)

```text
/etc/tw/config/                    # or C:\ProgramData\tw\config\ on Windows
├── config.yaml                    # active profile configuration
├── contexts.yaml                  # plaintext context index (active context + metadata)
├── contexts/                      # sealed context bundles (*.twctx)
├── id_ed25519                     # profile SSH private key
├── id_ed25519.pub                 # profile SSH public key (the profile identity)
├── ssh_host_ed25519_key           # SSH host key (server role)
├── authorized_keys                # client public keys (with permitopen; server role)
├── ca.crt                         # per-server CA cert (public part shipped to relay)
├── ca.key                         # per-server CA private key (never leaves server)
├── client.crt                     # client cert presented to relay mTLS gate (CN = server-id)
├── client.key                     # private key for client.crt
├── relay/                         # relay state + generated files (relay role)
│   ├── main.tf                    # provider-specific Terraform (cloud providers)
│   ├── cloud-init.yaml            # rendered cloud-init
│   ├── terraform.tfstate
│   ├── terraform.tfvars           # cloud credentials (Hetzner/DO only)
│   ├── relay-meta.json            # cloud relay metadata (name, ssh_open, ...)
│   └── manual-relay.json          # manual relay marker (domain, ip, ssh_open)
├── servers/                       # enrolled-server registry (relay role)
└── users/                         # per-user client configs (server role)
    └── alice/
        ├── config.yaml            # client config (exported via tw config export-user)
        ├── id_ed25519             # client private key
        ├── id_ed25519.pub         # client public key
        └── .applied               # marker: user registered on current relay
```

## File Layout (Client)

```text
/etc/tw/config/                    # or C:\ProgramData\tw\config\ on Windows
├── config.yaml                    # client configuration (imported context bundle)
├── contexts.yaml                  # context index
├── contexts/                      # sealed context bundles (*.twctx)
├── id_ed25519                     # client SSH private key (from the bundle)
├── id_ed25519.pub                 # client SSH public key
├── client.crt                     # per-server client cert for relay mTLS (from the bundle)
└── client.key                     # private key for client.crt (from the bundle)
```

!!! note "Override"
    Set `TW_CONFIG_DIR` environment variable (or the `--config-dir` flag) to use a custom config directory.

---

## Terraform Templates

Provider-specific Terraform files are embedded in the Go binary via `go:embed` and written to the relay directory during provisioning:

| Provider | Template | Instance | Firewall |
| -------- | -------- | -------- | -------- |
| AWS | `aws.tf.tmpl` | `t3.micro`, Ubuntu 24.04, `us-east-1` | Security group: 80 + 443 ingress (+ 22 with `--ssh-open`) |
| Hetzner | `hetzner.tf.tmpl` | `cx22`, Ubuntu 24.04, `nbg1` | Hetzner firewall: 80 + 443 (+ 22 with `--ssh-open`) |
| DigitalOcean | `digitalocean.tf.tmpl` | `s-1vcpu-1gb`, Ubuntu 24.04, `fra1` | DO firewall: 80 + 443 (+ 22 with `--ssh-open`) |

All templates use `user_data = file("${path.module}/cloud-init.yaml")` and output `relay_ip`. Templates are rendered through Go templates (for the `--ssh-open` conditionals) before being written to the relay directory. A fourth option, **Manual**, skips Terraform entirely: `tw relay create` (or the dashboard) generates an idempotent install script from `install-script.sh.tmpl` for a bring-your-own Ubuntu VM.

!!! warning "Xray version pinning"
    The Xray version installed on the relay is pinned to `v26.6.27` via the `terraform.XrayVersion` constant in `generate.go`. This version is baked into both the cloud-init template and the manual install script via the `--version` flag on the official Xray installer. It must stay protocol-compatible with the in-process `xray-core` dependency in `go.mod` (currently a pinned upstream mtls-branch commit).

---

## Building

Requires **Go 1.26+**.

### Version Injection

The `Version` variable in `internal/version/version.go` defaults to `"dev"` and is overridden at build time via `-ldflags`:

```bash
go build -ldflags "-X github.com/tunnelwhisperer/tw/internal/version.Version=v1.2.3" ./cmd/tw
```

The Makefile auto-detects the version from the latest git tag (`git describe --tags --always --dirty`). Override with `make build VERSION=v1.2.3`. The GitHub Actions release workflow injects the exact tag name (e.g. `v1.2.3`) from `github.ref_name`.

The version is used in:

- `tw --version` — CLI version output
- `/api/status` — `version` field in the JSON response

### Makefile Targets

| Target | Command | Description |
| ------ | ------- | ----------- |
| `make build` | `go build -ldflags "..." -o bin/tw ./cmd/tw` | Build for current platform (version auto-detected) |
| `make build-linux` | `GOOS=linux GOARCH=amd64 go build -ldflags "..." ...` | Cross-compile for Linux amd64 |
| `make build-windows` | `GOOS=windows GOARCH=amd64 go build -ldflags "..." ...` | Cross-compile for Windows amd64 |
| `make build-darwin` | `GOOS=darwin GOARCH=amd64 go build -ldflags "..." ...` | Cross-compile for macOS amd64 |
| `make build-all` | | Build Linux, Windows, and macOS |
| `make run` | Build + execute `./bin/tw` | Build and run |
| `make clean` | `rm -rf bin/` | Remove build artifacts |
| `make proto` | `protoc --go_out=... --go-grpc_out=...` | Regenerate gRPC stubs from `.proto` (rarely needed — wire format is JSON) |
| `make e2e` | Build + `docker compose up` + `go test -tags e2e` | Full-product e2e suite (12 scenarios); `E2E_KEEP=1` leaves the topology up |
| `make e2e-up` / `e2e-down` | | Start / tear down the e2e Docker Compose topology |

---

## What `tw server start` Starts

```mermaid
flowchart TD
    START([tw server start]) --> LOAD[Load / create config.yaml]
    LOAD --> DASH["Start dashboard<br/><i>if dashboard_port set</i>"]
    DASH --> KEYS{Keys exist?}
    KEYS -->|No| GEN["Generate ed25519 key pair + host key<br/>+ CA/client cert + seed authorized_keys"]
    KEYS -->|Yes| HASH
    GEN --> HASH["Save config file hash as cfgHash"]
    HASH --> SSHD["Start SSH Server :2222<br/><i>dynamic auth + permitopen</i>"]
    SSHD --> RELAY{relay_host<br/>configured?}
    RELAY -->|No| GRPC
    RELAY -->|Yes| UUID{UUID exists?}
    UUID -->|No| GENUUID[Generate UUID, save to config]
    UUID -->|Yes| XRAY
    GENUUID --> XRAY
    XRAY["Start Xray in-process<br/><i>dokodemo :2223 → relay SSH :22 (mTLS)</i>"]
    XRAY --> RTUNNEL["Open SSH reverse tunnel<br/><i>-R remote_port:localhost:ssh_port</i>"]
    RTUNNEL --> GRPC["Start gRPC API :50051"]
    GRPC --> READY([Server Ready])

    style START fill:#1565C0,color:#fff
    style READY fill:#00897B,color:#fff
    style SSHD fill:#1976D2,color:#fff
    style XRAY fill:#1976D2,color:#fff
    style RTUNNEL fill:#1976D2,color:#fff
    style GRPC fill:#1976D2,color:#fff
```

1. Loads (or creates) `config.yaml` from the platform config directory
2. Starts the **dashboard** if `server.dashboard_port` is set (so progress is visible during startup)
3. Generates an ed25519 SSH key pair (`id_ed25519` / `id_ed25519.pub`), an ed25519 host key (`ssh_host_ed25519_key`), and the per-server CA + mTLS client certificate if missing; seeds `authorized_keys` with the server's own public key
4. Saves `config.FileHash()` as `cfgHash` for change detection
5. Starts the **embedded SSH server** on the configured port (default `:2222`)
    - Dynamic `authorized_keys` -- re-read on every authentication attempt
    - `permitopen` / `single-session` enforcement per client key
6. If `xray.relay_host` is set:
    - Generates UUID if missing and saves to config
    - Starts **Xray** in-process (dokodemo-door on `sshPort+1`, or `server.xray_port` -> VLESS/XHTTP/mTLS to relay on `/tw/<server-id>`)
    - Opens an **SSH reverse tunnel** through Xray to the relay (`-R remote_port:localhost:ssh_port`)
    - Kicks off a background check that the relay's Xray stats policy is in place (`EnsureRelayStats`)
7. Starts the **gRPC API server** on the configured port (default `:50051`)

## What `tw client connect` Starts

```mermaid
flowchart TD
    START([tw client connect]) --> LOAD[Load config, ensure keys exist]
    LOAD --> VALIDATE["Validate relay_host +<br/>at least one tunnel mapping"]
    VALIDATE --> UUID{UUID exists?}
    UUID -->|No| GENUUID[Generate UUID, save to config]
    UUID -->|Yes| HASH
    GENUUID --> HASH
    HASH["Save config file hash as cfgHash"]
    HASH --> XRAY["Start Xray client<br/><i>dokodemo :54001 → relay :remote_port (mTLS)</i>"]
    XRAY --> SSH["SSH via Xray tunnel<br/><i>public key auth</i>"]
    SSH --> PORTS["Start local port listeners<br/><i>:5432, :8080, ...</i>"]
    PORTS --> BLOCK(["Block — auto-reconnect on failure"])

    style START fill:#00897B,color:#fff
    style BLOCK fill:#00897B,color:#fff
    style XRAY fill:#00ACC1,color:#fff
    style SSH fill:#00ACC1,color:#fff
    style PORTS fill:#00ACC1,color:#fff
```

1. Loads config and ensures keys exist
2. Validates that `relay_host` and at least one tunnel mapping are configured
3. Generates UUID if missing
4. Saves `config.FileHash()` as `cfgHash` for change detection
5. Starts **Xray** in client mode (dokodemo-door on `client.xray_port`, default `:54001` -> VLESS/XHTTP/mTLS to relay, targeting `client.server_ssh_port`)
6. Opens a **single SSH session** through Xray to the server's embedded SSH (public key auth)
7. Starts **local port listeners** for all configured tunnel mappings (bound to `client.listen_address`), forwarding through the SSH session
8. Blocks until stopped (Ctrl-C), with automatic reconnection on failure
