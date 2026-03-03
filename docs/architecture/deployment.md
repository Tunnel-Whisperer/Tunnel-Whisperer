# Deployment View

## Configuration

Default `config.yaml`:

```yaml
mode: ""                           # "server" or "client" (enforced by CLI)
log_level: info                    # debug, info, warn, error
proxy: ""                          # e.g. "socks5://user:pass@host:port" or "http://host:port"

xray:
  uuid: ""                         # auto-generated on first run
  relay_host: ""                   # e.g. relay.example.com
  relay_port: 443
  path: /tw

server:                            # only needed for `tw serve`
  ssh_port: 2222
  api_port: 50051
  dashboard_port: 8080
  relay_ssh_port: 22
  relay_ssh_user: ubuntu
  remote_port: 2222                # port exposed on relay for clients
  temp_xray_port: 59000            # temp tunnel for relay config updates

client:                            # only needed for `tw connect`
  ssh_user: tunnel
  server_ssh_port: 2222            # server's SSH port on relay
  tunnels:
    - local_port: 5432             # listen on client localhost
      remote_host: 127.0.0.1      # target on server (localhost only)
      remote_port: 5432            # PostgreSQL
```

!!! note "Mode field"
    The `mode` field prevents accidental cross-mode usage. When set, `requireMode()` in the CLI rejects server commands in client mode and vice versa. The dashboard adapts its UI based on this field.

---

## File Layout (Server)

```text
/etc/tw/config/                    # or C:\ProgramData\tw\config\ on Windows
├── config.yaml                    # server configuration
├── id_ed25519                     # server SSH private key
├── id_ed25519.pub                 # server SSH public key
├── ssh_host_ed25519_key           # SSH host key
├── authorized_keys                # client public keys (with permitopen)
├── relay/                         # Terraform state + generated files
│   ├── main.tf                    # provider-specific Terraform
│   ├── cloud-init.yaml            # rendered cloud-init
│   ├── terraform.tfstate
│   └── terraform.tfvars           # cloud credentials (Hetzner/DO only)
└── users/                         # per-user client configs
    └── alice/
        ├── config.yaml            # client config (send to client)
        ├── id_ed25519             # client private key (send to client)
        ├── id_ed25519.pub         # client public key
        └── .applied               # marker: user registered on current relay
```

## File Layout (Client)

```text
/etc/tw/config/                    # or C:\ProgramData\tw\config\ on Windows
├── config.yaml                    # client configuration (from server admin)
├── id_ed25519                     # client SSH private key (from server admin)
└── id_ed25519.pub                 # client SSH public key
```

!!! note "Override"
    Set `TW_CONFIG_DIR` environment variable to use a custom config directory.

---

## Terraform Templates

Provider-specific Terraform files are embedded in the Go binary via `go:embed` and written to the relay directory during provisioning:

| Provider | Template | Instance | Firewall |
| -------- | -------- | -------- | -------- |
| AWS | `aws.tf.tmpl` | `t3.micro`, Ubuntu 24.04, `us-east-1` | Security group: 80 + 443 ingress |
| Hetzner | `hetzner.tf.tmpl` | `cx22`, Ubuntu 24.04, `nbg1` | Hetzner firewall: 80 + 443 |
| DigitalOcean | `digitalocean.tf.tmpl` | `s-1vcpu-1gb`, Ubuntu 24.04, `fra1` | DO firewall: 80 + 443 |

All templates use `user_data = file("${path.module}/cloud-init.yaml")` and output `relay_ip`.

!!! warning "Xray version pinning"
    The Xray version installed on the relay is pinned to `v26.2.6` via the `terraform.XrayVersion` constant in `generate.go`. This version is baked into both the cloud-init template and the manual install script via the `--version` flag on the official Xray installer. Keeping this in sync with the `xray-core` dependency in `go.mod` ensures protocol compatibility between the in-process Xray (server/client) and the relay's Xray.

---

## Building

Requires **Go 1.25+**.

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
| `make proto` | `protoc --go_out=... --go-grpc_out=...` | Regenerate gRPC stubs from `.proto` |

---

## What `tw serve` Starts

```mermaid
flowchart TD
    START([tw serve]) --> LOAD[Load / create config.yaml]
    LOAD --> KEYS{Keys exist?}
    KEYS -->|No| GEN["Generate ed25519 key pair<br/>+ host key + seed authorized_keys"]
    KEYS -->|Yes| HASH
    GEN --> HASH["Save config file hash as cfgHash"]
    HASH --> INIT[Initialize core service]
    INIT --> SSHD["Start SSH Server :2222<br/><i>dynamic auth + permitopen</i>"]
    SSHD --> RELAY{relay_host<br/>configured?}
    RELAY -->|No| GRPC
    RELAY -->|Yes| UUID{UUID exists?}
    UUID -->|No| GENUUID[Generate UUID, save to config]
    UUID -->|Yes| XRAY
    GENUUID --> XRAY
    XRAY["Start Xray in-process<br/><i>dokodemo :2223 → relay SSH :22</i>"]
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
2. Generates an ed25519 SSH key pair (`id_ed25519` / `id_ed25519.pub`) if missing
3. Generates an ed25519 host key (`ssh_host_ed25519_key`) for the embedded SSH server
4. Seeds `authorized_keys` with the server's own public key
5. Saves `config.FileHash()` as `cfgHash` for change detection
6. Initializes the core service
7. Starts the **embedded SSH server** on the configured port (default `:2222`)
    - Dynamic `authorized_keys` -- re-read on every authentication attempt
    - `permitopen` enforcement -- restricts port forwarding per client key
8. If `xray.relay_host` is set:
    - Generates UUID if missing and saves to config
    - Starts **Xray** in-process (dokodemo-door on `sshPort+1` -> VLESS/XHTTP/TLS to relay)
    - Opens an **SSH reverse tunnel** through Xray to the relay (`-R remote_port:localhost:ssh_port`)
9. Starts the **gRPC API server** on the configured port (default `:50051`)

## What `tw connect` Starts

```mermaid
flowchart TD
    START([tw connect]) --> LOAD[Load config, ensure keys exist]
    LOAD --> VALIDATE["Validate relay_host +<br/>at least one tunnel mapping"]
    VALIDATE --> UUID{UUID exists?}
    UUID -->|No| GENUUID[Generate UUID, save to config]
    UUID -->|Yes| HASH
    GENUUID --> HASH
    HASH["Save config file hash as cfgHash"]
    HASH --> XRAY["Start Xray client<br/><i>dokodemo :54001 → relay :2222</i>"]
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
5. Starts **Xray** in client mode (dokodemo-door on `:54001` -> VLESS/XHTTP/TLS to relay, targeting `server_ssh_port`)
6. Opens a **single SSH session** through Xray to the server's embedded SSH (public key auth)
7. Starts **local port listeners** for all configured tunnel mappings, forwarding through the SSH session
8. Blocks until stopped (Ctrl-C), with automatic reconnection on failure
