# Tunnel Whisperer

**Surgical, resilient connectivity for restrictive enterprise environments.**

Tunnel Whisperer creates **port-to-port bridges** across separated private networks, encapsulated in standard HTTPS to traverse firewalls, NAT, and Deep Packet Inspection.

---

## How It Works

```
Client Network                   Public Cloud                    Server Network
+--------------+             +------------------+             +--------------+
|  tw connect  |-- HTTPS -->|     Relay VM      |<-- HTTPS --|   tw serve   |
|              |   (Xray     |                  |   (Xray     |              |
| local ports  |   VLESS +   |  Caddy :443      |   VLESS +   | SSH server   |
| :5432 :3389  |   splitHTTP)|  reverse proxy   |   splitHTTP)| :2222        |
|              |             |  Xray :10000     |             |              |
|  SSH --------+-------------+------------------+-------------+> port fwd    |
|  (over Xray) |             |  SSH :22 (local) |             | -> services  |
+--------------+             |  Firewall: 80+443|             +--------------+
                             +------------------+
```

Both server and client connect **outbound** to a lightweight relay VM on port 443. The relay never sees plaintext — it forwards encrypted streams between the two sides.

---

## Key Properties

- **Zero inbound ports** — all connections are outbound to :443
- **DPI resistant** — traffic is indistinguishable from regular HTTPS
- **Per-user lockdown** — each client can only reach explicitly allowed ports via `permitopen`
- **End-to-end encryption** — SSH inside Xray inside TLS; the relay is just a passthrough
- **Automatic reconnection** — exponential backoff (2s → 30s max) on both sides
- **Web dashboard** — manage relay, users, and tunnels from a browser

---

## Use Cases

### Healthcare Interoperability

Forward DICOM/HL7 ports from a hospital scanner to a cloud AI platform — through a firewall that only allows HTTPS. Deploy a small gateway on the scanner's LAN; the scanner sends to `localhost`, and the tunnel delivers it to the cloud.

### Vendor Remote Support

Give a vendor surgical access to a single maintenance port on a factory-floor PLC — without VPN, without inbound firewall rules, and without exposing the rest of the network.

### Developer & Data Science Workflows

Connect a cloud Jupyter notebook to an on-premise database behind a corporate firewall. The notebook queries `localhost:5432` as if the database were local.

---

## Quick Start

=== "Server"

    ```bash
    # Build
    go build -o bin/tw ./cmd/tw

    # Provision a relay VM (Hetzner, DigitalOcean, or AWS)
    ./bin/tw create relay-server

    # Create a client user with port restrictions
    ./bin/tw create user

    # Start the server
    ./bin/tw serve
    ```

    See [Server Setup](getting-started/server-setup.md) for the full walkthrough.

=== "Client"

    ```bash
    # Place the config zip from the server admin
    ./bin/tw connect
    ```

    See [Client Setup](getting-started/client-setup.md) for details.

=== "Dashboard"

    ```bash
    ./bin/tw dashboard
    ```

    Open `http://localhost:8080` to manage everything from a browser. See [Web Dashboard](guides/dashboard.md).

---

## Documentation

| Section | What's Inside |
| ------- | ------------- |
| [Getting Started](getting-started/index.md) | Prerequisites, installation, server and client setup |
| [Guides](guides/relay-provisioning.md) | Relay provisioning, user management, dashboard, proxy, troubleshooting |
| [Reference](reference/cli.md) | CLI commands, configuration, API endpoints, file layout |
| [Architecture](architecture/index.md) | arc42 documentation with sequence diagrams and component views |
| [Security](security/index.md) | Encryption layers, access control, compliance properties |

---

## Market Comparison

| Feature | **Tunnel Whisperer** | **Standard VPNs** (Tailscale/WireGuard) | **Reverse Proxies** (Ngrok) |
| :--- | :--- | :--- | :--- |
| **Connectivity** | Surgical (port-to-port) | Broad (host-to-host) | Public (port-to-web) |
| **Network Compatibility** | High (DPI-resistant HTTPS) | Low (UDP/standard ports often blocked) | Medium (standard HTTPS) |
| **Deployment Target** | Gateway / sidecar (connects *other* devices) | Host-based (connects *this* device) | Dev/test (temporary exposure) |
| **Infrastructure** | Self-hosted (you own data/keys) | SaaS / hybrid | SaaS |
| **Primary Goal** | Production reliability in strict networks | Mesh networking | Public access |
