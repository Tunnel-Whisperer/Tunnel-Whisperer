# Relay Provisioning

The relay is a lightweight cloud VM that serves as the rendezvous point between server and clients. Both sides connect outbound to the relay over HTTPS — no inbound ports needed.

## Automated Provisioning (Terraform)

### CLI

```bash
tw create relay-server
```

### Dashboard

Navigate to **Relay** → **Provision Relay** and follow the wizard.

### Steps

The wizard walks through 8 steps:

1. **SSH key generation** — creates an ed25519 key pair if missing
2. **Xray UUID generation** — creates or reuses the server's transport UUID
3. **Relay domain** — sets `xray.relay_host` (e.g. `relay.example.com`)
4. **Cloud provider** — choose Hetzner, DigitalOcean, or AWS with region selection
5. **Credentials** — enter API token (Hetzner/DO) or Access Key + Secret (AWS)
6. **Credential test** — validates credentials via provider API
7. **Terraform provisioning** — generates cloud-init + Terraform config, runs `terraform init` and `terraform apply`
8. **DNS + HTTPS readiness** — prompts for DNS A record creation, then polls until the domain resolves and Caddy issues a TLS certificate

### What Gets Installed

The relay VM (Ubuntu 24.04) is configured via cloud-init to:

- Create an SSH user with the server's public key
- Install **Caddy** from the official apt repository (TLS termination)
- Install **Xray** at a pinned version (`v1.8.24`) for reproducibility
- Write Xray config: VLESS inbound on `127.0.0.1:10000` with splitHTTP transport
- Write Caddyfile: reverse proxy `<domain>/tw*` to Xray
- Lock SSH to `127.0.0.1` only, disable password auth
- Configure firewall: deny all incoming, allow 80/tcp + 443/tcp only

!!! info "Version pinning"
    Xray is installed at a pinned version matching the `xray-core` dependency in the Go binary. This ensures the relay stays compatible even when upstream releases new versions.

### Supported Providers

| Provider | Instance | Default Region | Credential |
| -------- | -------- | -------------- | ---------- |
| Hetzner | cx22 | nbg1 (Nuremberg) | API Token |
| DigitalOcean | s-1vcpu-1gb | fra1 (Frankfurt) | API Token |
| AWS | t3.micro | us-east-1 | Access Key + Secret Key |

### Re-provisioning

If a relay already exists (Terraform state present), the wizard offers to destroy and recreate it. TLS certificates are saved before destruction and restored on the new relay to avoid Let's Encrypt rate limits.

## Manual Setup

For existing VPS or unsupported providers:

1. In the dashboard, go to **Relay** → **Provision** → **Manual**
2. Enter your relay domain
3. Copy the generated install script
4. SSH into your VPS and run the script as root
5. Create a DNS A record pointing your domain to the VPS IP
6. Back in the dashboard, enter the IP address to save the relay configuration

!!! warning "SSH access"
    The install script locks down SSH to localhost only. After running it, you can only access the relay via `tw relay ssh` through the Xray tunnel.

## Testing the Relay

```bash
tw test relay
```

This runs a 3-step diagnostic:

1. **DNS resolution** — verifies the domain resolves to the expected IP
2. **HTTPS/Caddy** — confirms Caddy is serving with a valid TLS certificate
3. **Xray + SSH** — connects through the full Xray tunnel and opens an SSH session

## Destroying the Relay

```bash
tw destroy relay-server
```

This saves TLS certificates for reuse, then runs `terraform destroy` to remove the cloud infrastructure. Users are marked as inactive (their relay UUIDs become invalid).
