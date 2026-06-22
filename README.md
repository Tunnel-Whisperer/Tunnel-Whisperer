<p align="center">
  <img src="docs/assets/icon.svg" alt="Tunnel Whisperer" width="180"/>
</p>

<h1 align="center">Tunnel Whisperer</h1>

<p align="center"><strong>Surgical, resilient connectivity for restrictive enterprise environments.</strong></p>

<p align="center">
  <a href="https://github.com/Tunnel-Whisperer/Tunnel-Whisperer"><img src="https://img.shields.io/badge/Status-Alpha-yellow" alt="Status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue" alt="License"></a>
</p>

Tunnel Whisperer creates **resilient, application-layer bridges** for specific ports across separated private networks. It encapsulates traffic in standard HTTPS to traverse firewalls, NAT, and Deep Packet Inspection (DPI).

> **[Full Documentation](https://tunnel-whisperer.github.io/Tunnel-Whisperer)** — getting started, guides, architecture, API reference, and more.

---

## The Problem: "The Connectivity Gap"

In modern enterprise environments (Healthcare, Manufacturing, Finance), connectivity is blocked by rigid network policies:

1. **Strict Egress Rules:** Firewalls block everything except Port 443 (HTTPS). SSH, OpenVPN, and WireGuard are dropped.
2. **Legacy Devices:** MRI scanners, industrial PLCs, and old servers cannot install modern VPN clients.
3. **DPI Interference:** "Next-Gen" firewalls detect and kill non-web traffic even on Port 443.

**Tunnel Whisperer bridges this gap.** It wraps TCP traffic inside a genuine TLS-encrypted HTTPS stream using Xray's VLESS+XHTTP protocol. To the network, it looks exactly like standard web traffic.

---

## Use Cases

### Healthcare Interoperability (DICOM/HL7)

Forward DICOM port 104 from a hospital scanner to a cloud AI platform — through a firewall that only allows HTTPS. Deploy a gateway on the scanner's LAN; the scanner sends to `localhost`, and the tunnel delivers it to the cloud.

### Vendor Remote Support (OT/IoT)

Give a vendor surgical access to a single maintenance port on a factory-floor PLC — without VPN, without inbound firewall rules, without exposing the rest of the network.

### Developer & Data Science Workflows

Connect a cloud Jupyter notebook to an on-premise database behind a corporate firewall. Query `localhost:5432` as if the database were local.

---

## Architecture

```
Client Network                   Public Cloud                    Server Network
+--------------+             +------------------+             +--------------+
|  tw connect  |-- HTTPS --> |     Relay VM     | <-- HTTPS --|   tw serve   |
|              |   (Xray     |                  |   (Xray     |              |
| local ports  |   VLESS +   |  Caddy :443      |   VLESS +   | SSH server   |
| :5432 :3389  |   XHTTP)    |  reverse proxy   |   XHTTP)    | :2222        |
|              |             |  Xray :10000     |             |              |
|  SSH --------+-------------+------------------+-------------+> port fwd    |
|  (over Xray) |             |  SSH :22 (local) |             | -> services  |
+--------------+             |  Firewall: 80+443|             +--------------+
                             +------------------+
```

1. **Transport:** Xray VLESS + XHTTP + mutual TLS on port 443 — indistinguishable from regular HTTPS
2. **Relay:** Lightweight cloud VM (Hetzner, DigitalOcean, or AWS) with Caddy (TLS/ACME) and Xray. Caddy enforces mutual TLS (`client_auth require_and_verify`) against a per-server CA, so only certificate-bearing servers are admitted. SSH on localhost only.
3. **Tunnel:** Embedded SSH server (Go `x/crypto/ssh`) handles port forwarding, encryption, and per-user auth

**Key properties:**
- Zero inbound ports — all connections outbound to :443
- Mutual-TLS relay admission — a per-server X.509 client certificate is required at the TLS handshake
- End-to-end SSH encryption — the relay never sees plaintext
- Per-user port lockdown via `permitopen` in `authorized_keys`
- Automatic reconnection with exponential backoff (2s → 30s max)

> See [Architecture Documentation](https://tunnel-whisperer.github.io/Tunnel-Whisperer/architecture/) for sequence diagrams, component views, and deployment details.

---

## Video Tutorial

New to Tunnel Whisperer? Watch the step-by-step video walkthrough:

[![Video Tutorial](https://img.youtube.com/vi/cIe0-C1IMe4/maxresdefault.jpg)](https://www.youtube.com/watch?v=cIe0-C1IMe4)

---

## Quick Start

Requires **Go 1.26+** and **Terraform** (for relay provisioning).

```bash
# Build
make build
# Raw build:
#   go build -o bin/tw ./cmd/tw

# Server side
tw create relay-server    # 8-step wizard: provision relay VM
tw create user            # 5-step wizard: create client with port restrictions
tw serve                  # start server

# Client side
tw connect                # connect using config from server admin

# Web dashboard
tw dashboard              # manage everything from a browser

# Install as system service (Linux systemd / Windows SCM)
sudo tw service install   # register and enable the service
sudo tw service start     # start the service
```

> See [Getting Started](https://tunnel-whisperer.github.io/Tunnel-Whisperer/getting-started/) for the full walkthrough.

---

## CLI Commands

| Command | Description |
|---------|-------------|
| `tw serve` | Start the server (SSH, Xray tunnel, reverse port forward, gRPC API) |
| `tw connect` | Connect as a client (Xray tunnel, SSH port forwarding) |
| `tw dashboard` | Start the web dashboard |
| `tw status` | Show current mode, relay info, and server/client state |
| `tw create relay-server` | Provision a relay VM (Hetzner, DigitalOcean, or AWS) |
| `tw create user` | Create a client user with port restrictions |
| `tw list users` | List all configured users |
| `tw delete user <name>` | Delete a user and revoke access |
| `tw export user <name>` | Export user config as zip |
| `tw test relay` | Run 3-step relay connectivity diagnostic |
| `tw relay ssh` | Open SSH terminal to relay through Xray tunnel |
| `tw destroy relay-server` | Tear down relay infrastructure |
| `tw proxy [set\|clear]` | Manage SOCKS5/HTTP proxy for tunnel traffic |
| `tw service install` | Install tw as a system service (Linux systemd / Windows SCM) |
| `tw service uninstall` | Remove the system service |
| `tw service start` | Start the system service |
| `tw service stop` | Stop the system service |

> See [CLI Reference](https://tunnel-whisperer.github.io/Tunnel-Whisperer/reference/cli/) for details and flags.

---

## Security Model

| Layer | Standard | Purpose |
| ----- | -------- | ------- |
| TLS 1.3 + mTLS | Industry standard + X.509 | Encrypts all data in transit; admits only certificate-bearing connections at the relay |
| VLESS + XHTTP | Tunnel protocol | Tags users, obfuscates traffic patterns (defense-in-depth) |
| Ed25519 SSH | Elliptic curve cryptography | Authenticates endpoints, restricts per-user access |

- **Mutual-TLS admission** — a per-server CA-issued client certificate gates the relay at the TLS handshake
- **Zero plaintext** leaves the local network
- **No signing keys** on the relay — it holds only public CA certificates; compromise does not expose user data
- **Least privilege** — each user can only forward to explicitly allowed ports
- **Dynamic keys** — add/revoke users without restarting the server

> See [Security Documentation](https://tunnel-whisperer.github.io/Tunnel-Whisperer/security/) for encryption details, access control, and compliance properties.

---

## Market Comparison

| Feature | **Tunnel Whisperer** | **Standard VPNs** (Tailscale/WireGuard) | **Reverse Proxies** (Ngrok) |
| :--- | :--- | :--- | :--- |
| **Connectivity** | Surgical (port-to-port) | Broad (host-to-host) | Public (port-to-web) |
| **Network Compatibility** | High (DPI-resistant HTTPS) | Low (UDP/standard ports often blocked) | Medium (standard HTTPS) |
| **Deployment Target** | Gateway / sidecar (connects *other* devices) | Host-based (connects *this* device) | Dev/test (temporary exposure) |
| **Infrastructure** | Self-hosted (you own data/keys) | SaaS / hybrid | SaaS |
| **Primary Goal** | Production reliability in strict networks | Mesh networking | Public access |
