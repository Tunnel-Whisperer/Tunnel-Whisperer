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
    The Xray version installed on the relay is pinned to `v1.8.24` via the `terraform.XrayVersion` constant in `generate.go`. This version is baked into both the cloud-init template and the manual install script via the `--version` flag on the official Xray installer. Keeping this in sync with the `xray-core` dependency in `go.mod` ensures protocol compatibility between the in-process Xray (server/client) and the relay's Xray.

---

## Building

Requires **Go 1.22+**.

### Makefile Targets

| Target | Command | Description |
| ------ | ------- | ----------- |
| `make build` | `go build -o bin/tw ./cmd/tw` | Build for current platform |
| `make build-linux` | `GOOS=linux GOARCH=amd64 go build ...` | Cross-compile for Linux amd64 |
| `make build-windows` | `GOOS=windows GOARCH=amd64 go build ...` | Cross-compile for Windows amd64 |
| `make build-all` | | Build both Linux and Windows |
| `make run` | Build + execute `./bin/tw` | Build and run |
| `make clean` | `rm -rf bin/` | Remove build artifacts |
| `make proto` | `protoc --go_out=... --go-grpc_out=...` | Regenerate gRPC stubs from `.proto` |

---

## What `tw serve` Starts

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
    - Starts **Xray** in-process (dokodemo-door on `sshPort+1` -> VLESS/splitHTTP/TLS to relay)
    - Opens an **SSH reverse tunnel** through Xray to the relay (`-R remote_port:localhost:ssh_port`)
9. Starts the **gRPC API server** on the configured port (default `:50051`)

## What `tw connect` Starts

1. Loads config and ensures keys exist
2. Validates that `relay_host` and at least one tunnel mapping are configured
3. Generates UUID if missing
4. Saves `config.FileHash()` as `cfgHash` for change detection
5. Starts **Xray** in client mode (dokodemo-door on `:54001` -> VLESS/splitHTTP/TLS to relay, targeting `server_ssh_port`)
6. Opens a **single SSH session** through Xray to the server's embedded SSH (public key auth)
7. Starts **local port listeners** for all configured tunnel mappings, forwarding through the SSH session
8. Blocks until stopped (Ctrl-C), with automatic reconnection on failure
