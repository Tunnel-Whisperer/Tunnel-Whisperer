# Getting Started

Tunnel Whisperer connects services across separated private networks via resilient HTTPS tunnels. One binary, three roles: the **relay** admin owns the relay VM, **servers** join it and expose services, **clients** import a bundle and connect. This guide walks you through the setup.

## Prerequisites

- **Go 1.26+** — to build from source
- **Terraform >= 1.0** (tested with 1.16) — only for automated cloud relay provisioning; see [Installation](installation.md#terraform-cloud-relays-only)
- **A domain name** — pointed at your relay VM (e.g. `relay.example.com`)
- **A cloud account** — Hetzner, DigitalOcean, or AWS (for automated provisioning), or any VM with a public IP

## Workflow Overview

```text
                              # role: relay (admin machine)
1. Provision   tw relay create
2. Invite      tw relay invite            (mints a code, waits for the server)

                              # role: server
3. Join        tw join relay.example.com <code>
4. Invite      tw server user invite alice -m 8080:80
               (mints a code, waits for the client)
5. Run         tw server start                 (or tw dashboard / tw service install)

                              # role: client
6. Connect     tw join relay.example.com <code>
               tw client connect
```

The **relay admin** provisions the relay and invites servers. Each **server** operator redeems an invite to join the relay, invites its own users, and runs `tw server start`. Each **client** redeems a user invite — which generates their identity locally and never transmits a private key — and runs `tw client connect` to establish local port forwarding. Every invite is a spoken one-time code confirmed by a short authentication string read back between the two humans involved; see [Tenants — security](../relay/tenants.md#security).

!!! warning "The first role command sets the machine's mode"
    A machine's mode (`relay`, `server`, or `client`) is set by its first role action and is signed for tamper evidence — commands of other roles refuse to run. See [Global — the role model](../global/index.md).

## Pick Your Role

- [Global](../global/index.md) — the role model, contexts, status, service, proxy, dashboard (everyone)
- [Relay](../relay/index.md) — provision the relay, enroll and manage servers
- [Server](../server/index.md) — join a relay, manage users, run the tunnel
- [Client](../client/index.md) — import a bundle and connect

## Next Steps

- [Installation](installation.md) — build from source, cross-compile, install as a service
- [Multi-Server Walkthrough](../guides/multi-server-walkthrough.md) — a complete from-scratch setup: 1 relay, 2 servers, 2 clients
