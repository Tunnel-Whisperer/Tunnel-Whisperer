# Getting Started

Tunnel Whisperer connects services across separated private networks via resilient HTTPS tunnels. One binary, three roles: the **relay** admin owns the relay VM, **servers** join it and expose services, **clients** import a bundle and connect. This guide walks you through the setup.

## Prerequisites

- **Go 1.26+** — to build from source
- **Terraform** — for automated cloud relay provisioning (not needed for the manual bring-your-own-VM path)
- **A domain name** — pointed at your relay VM (e.g. `relay.example.com`)
- **A cloud account** — Hetzner, DigitalOcean, or AWS (for automated provisioning), or any VM with a public IP

## Workflow Overview

```text
                              # role: relay (admin machine)
1. Provision   tw relay create
2. Enroll      tw relay enroll-server tw_join_<id>.json

                              # role: server
3. Join        tw server join-relay relay.example.com
               tw server join-relay --apply tw_join_response_<id>.json
4. Users       tw server user create alice -m 8080:80
               tw server user apply alice
               tw config export-user alice
5. Run         tw server start                 (or tw dashboard / tw service install)

                              # role: client
6. Connect     tw config import alice-tw-context.twctx --activate
               tw client connect
```

The **relay admin** provisions the relay and enrolls servers. Each **server** operator joins the relay, creates users, and runs `tw server start`. Each **client** receives a context bundle (`.twctx`), imports it, and runs `tw client connect` to establish local port forwarding.

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
