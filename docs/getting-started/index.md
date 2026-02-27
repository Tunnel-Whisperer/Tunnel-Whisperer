# Getting Started

Tunnel Whisperer connects services across separated private networks via resilient HTTPS tunnels. This guide walks you through the setup.

## Prerequisites

- **Go 1.22+** — to build from source
- **Terraform** — for automated relay provisioning (optional if using manual setup)
- **A domain name** — pointed at your relay VM (e.g. `relay.example.com`)
- **A cloud account** — Hetzner, DigitalOcean, or AWS (for automated provisioning)

## Workflow Overview

```text
1. Build          go build -o bin/tw ./cmd/tw
2. Provision      tw create relay-server        (or manual install)
3. Create users   tw create user
4. Start server   tw serve                      (or tw dashboard)
5. Connect        tw connect                    (on client machine)
```

The **server** operator provisions a relay VM, creates users, and runs `tw serve`. Each **client** receives a config bundle (zip) and runs `tw connect` to establish local port forwarding.

## Next Steps

- [Installation](installation.md) — build from source, cross-compile, Makefile targets
- [Server Setup](server-setup.md) — provision relay, create users, start the server
- [Client Setup](client-setup.md) — upload config, connect, verify tunnels
