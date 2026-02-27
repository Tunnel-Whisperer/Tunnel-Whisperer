# File Layout

Tunnel Whisperer stores all runtime configuration, keys, and infrastructure
state under a single platform-specific directory.

| Platform | Base directory |
|---|---|
| Linux | `/etc/tw/config/` |
| Windows | `C:\ProgramData\tw\config\` |
| Override | `TW_CONFIG_DIR` environment variable |

---

## Server file tree

A fully provisioned server with two users has the following layout:

```
/etc/tw/config/
├── config.yaml              # Main configuration file
├── authorized_keys          # SSH authorized keys (auto-generated from users)
├── ssh_host_ed25519_key     # SSH server host key (private)
├── ssh_host_ed25519_key.pub # SSH server host key (public)
├── relay/
│   ├── main.tf              # Terraform configuration for the relay
│   ├── cloud-init.yaml      # Cloud-init script (Caddy + Xray + SSH setup)
│   ├── terraform.tfvars     # Terraform variables (provider, domain, token)
│   └── terraform.tfstate    # Terraform state (tracks provisioned resources)
└── users/
    ├── alice/
    │   ├── config.yaml      # Client config pre-filled for this user
    │   ├── id_ed25519       # SSH private key
    │   └── id_ed25519.pub   # SSH public key
    └── bob/
        ├── config.yaml      # Client config pre-filled for this user
        ├── id_ed25519       # SSH private key
        └── id_ed25519.pub   # SSH public key
```

## Client file tree

A client receives a config bundle from the server and places it in the config
directory:

```
/etc/tw/config/
├── config.yaml              # Client configuration (mode, xray, tunnels)
├── id_ed25519               # SSH private key (received from server)
└── id_ed25519.pub           # SSH public key (received from server)
```

---

## Per-user config bundle

When a user is created on the server, `tw export user <name>` (or the
dashboard download button) produces a `.zip` file containing everything
the client needs.

**Zip contents:**

```
<name>-tw-config.zip
├── config.yaml              # Complete client config
├── id_ed25519               # SSH private key for this user
└── id_ed25519.pub           # SSH public key for this user
```

The `config.yaml` inside the bundle is pre-filled with:

- `mode: client`
- `xray.uuid` -- unique UUID for this user
- `xray.relay_host` -- the server's relay domain
- `xray.relay_port` and `xray.path` -- transport settings
- `client.ssh_user` -- the user's name
- `client.server_ssh_port` -- matching the server's SSH port
- `client.tunnels` -- port mappings defined during user creation

!!! tip "Deploying the bundle"
    Extract the zip into the client's config directory and start the client:

    ```bash
    # Linux
    sudo mkdir -p /etc/tw/config
    sudo unzip alice-tw-config.zip -d /etc/tw/config/
    sudo tw connect
    ```

---

## Relay directory

The `relay/` subdirectory contains all Terraform-managed infrastructure files.
It is created during `tw create relay-server` and persists until
`tw destroy relay-server` removes the cloud resources.

| File | Description |
|---|---|
| `main.tf` | Terraform configuration defining the VPS, firewall rules, and DNS |
| `cloud-init.yaml` | Cloud-init user data that installs Caddy, Xray, and configures SSH |
| `terraform.tfvars` | Input variables: provider credentials, domain, region |
| `terraform.tfstate` | Terraform state file tracking all provisioned cloud resources |

!!! warning "Do not edit `terraform.tfstate`"
    The state file is managed by Terraform. Manual edits can cause resource
    drift or prevent clean destruction of the relay server.
