# Tunnel Whisperer

**Surgical, resilient connectivity for restrictive enterprise environments.**

Tunnel Whisperer creates **port-to-port bridges** across separated private networks, encapsulated in standard HTTPS to traverse firewalls, NAT, and Deep Packet Inspection.

---

## How It Works

```
Client Network                   Public Cloud                    Server Network
+--------------+             +------------------+             +--------------+
|  tw client   |-- HTTPS --> |     Relay VM     |<-- HTTPS -- |  tw server   |
|  connect     |   (Xray     |                  |   (Xray     |  start       |
| local ports  |   VLESS +   |  Caddy :443      |   VLESS +   | SSH server   |
| :5432 :3389  |   XHTTP +   |  mTLS gate       |   XHTTP +   | :2222        |
|              |   mTLS)     |  Xray (loopback, |   mTLS)     |              |
|              |             |   per-tenant)    |             |              |
|  SSH --------+-------------+------------------+-------------+> port fwd    |
|  (over Xray) |             |  SSH :22 (local) |             | -> services  |
+--------------+             |  Firewall: 80+443|             +--------------+
```

Both server and client connect **outbound** to a lightweight relay VM on port 443. The relay admits tunnels by mutual TLS and never sees plaintext — it forwards encrypted streams between the two sides.

---

## Three Roles, One Binary

The same `tw` binary plays three mutually exclusive roles, selected by its configured (and cryptographically signed) mode:

- **Relay** — the admin who owns the relay VM: provisions it, holds the CA, enrolls servers (`tw relay …`)
- **Server** — the operator exposing services: joins a relay, creates users, runs the tunnel endpoint (`tw server …`)
- **Client** — the person connecting in: imports a user bundle, gets local port forwards (`tw client …`)

One relay serves many servers; each server serves many clients. See the [Global section](global/index.md) for the role model and everything shared across roles.

---

## Key Properties

- **Zero inbound ports** — all connections are outbound to :443
- **DPI resistant** — traffic is indistinguishable from regular HTTPS
- **mTLS admission** — the relay only accepts tunnels presenting a CA-issued client certificate
- **Per-user lockdown** — each client can only reach explicitly allowed ports via `permitopen`
- **End-to-end encryption** — SSH inside Xray inside TLS; the relay is just a passthrough
- **Automatic reconnection** — exponential backoff (2s → 30s max) on both sides
- **Contexts** — kubectl-style profiles: switch relays and identities with `tw config use-context`
- **Web dashboard** — manage relay, servers, users, and tunnels from a browser
- **System service** — run via systemd, Windows SCM, or launchd with auto-start on boot

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

=== "Relay (admin)"

    ```bash
    # Provision a relay VM (Hetzner, DigitalOcean, AWS — or bring your own)
    tw relay create

    # Enroll a server (using the join request it sends you)
    tw relay enroll-server tw_join_<server-id>.json

    # See your tenants
    tw relay get-servers
    ```

=== "Server"

    ```bash
    # Join a relay: generate a join request, apply the admin's response
    tw server join-relay relay.example.com
    tw server join-relay --apply tw_join_response_<server-id>.json

    # Create a client user and issue their bundle
    tw server user create alice -m 8080:80
    tw server user apply alice
    tw config export-user alice

    # Start the tunnel
    tw server start
    ```

=== "Client"

    ```bash
    # Import the bundle from the server operator, then connect
    tw config import alice-tw-context.twctx --activate
    tw client connect
    ```

=== "Dashboard"

    ```bash
    tw dashboard
    ```

    Open `http://localhost:8080` to manage everything from a browser. See [Web Dashboard](global/dashboard.md).

For the full five-machine walkthrough, see the [Multi-Server Walkthrough](guides/multi-server-walkthrough.md).

---

## Documentation — Pick Your Role

| Section | What's Inside |
| ------- | ------------- |
| [Global](global/index.md) | The role model, contexts, status, service, proxy, dashboard, completion — everything shared |
| [Relay](relay/index.md) | Provisioning, enrolling servers, relay SSH — for the relay admin |
| [Server](server/index.md) | Joining a relay, user and app management, running the tunnel — for server operators |
| [Client](client/index.md) | Importing bundles and connecting — for clients |
| [Getting Started](getting-started/index.md) | Prerequisites, installation, first setup |
| [Reference](reference/cli.md) | CLI commands, configuration, API endpoints, file layout |
| [Architecture](architecture/index.md) | arc42 documentation with sequence diagrams and component views |
| [Security](security/index.md) | Encryption layers, mTLS admission, access control, compliance properties |

---

## Market Comparison

| Feature | **Tunnel Whisperer** | **Standard VPNs** (Tailscale/WireGuard) | **Reverse Proxies** (Ngrok) |
| :--- | :--- | :--- | :--- |
| **Connectivity** | Surgical (port-to-port) | Broad (host-to-host) | Public (port-to-web) |
| **Network Compatibility** | High (DPI-resistant HTTPS) | Low (UDP/standard ports often blocked) | Medium (standard HTTPS) |
| **Deployment Target** | Gateway / sidecar (connects *other* devices) | Host-based (connects *this* device) | Dev/test (temporary exposure) |
| **Infrastructure** | Self-hosted (you own data/keys) | SaaS / hybrid | SaaS |
| **Primary Goal** | Production reliability in strict networks | Mesh networking | Public access |
