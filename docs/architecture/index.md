# Architecture Overview

Tunnel Whisperer creates resilient, application-layer bridges for specific ports across separated private networks. It encapsulates traffic in standard HTTPS to traverse strict firewalls, NAT, and DPI-controlled environments.

The system has three roles: a **relay** operator (admin) owns a publicly reachable relay VM, **servers** behind private networks join it via an enrollment handshake, and **clients** behind other private networks connect through it. The relay is multi-tenant — several independent servers can share one relay, each isolated behind its own path, CA, and loopback port. All connectivity is egress-only from servers and clients.

```mermaid
graph LR
    subgraph Server Network
        S[Server - tw server start]
    end

    subgraph Public Cloud
        R[Relay VM]
        C_[Caddy :443 mTLS gate]
        X["Xray (per-tenant VLESS inbounds<br/>on 127.0.0.1)"]
    end

    subgraph Client Network
        CL[Client - tw client connect]
    end

    S -- "mTLS :443 (VLESS+XHTTP, /tw/&lt;id&gt;)" --> C_
    CL -- "mTLS :443 (VLESS+XHTTP, /tw/&lt;id&gt;)" --> C_
    C_ -- "per-tenant handle /tw/&lt;id&gt;" --> X
    X -- "freedom outbound (loopback only)" --> R
```

## Documentation Sections

| Section | Description |
| ------- | ----------- |
| [System Context](system-context.md) | Goals, quality attributes, system scope, and protocol breakdown |
| [Solution Strategy](solution-strategy.md) | Challenge-to-solution mapping with technology choices |
| [Building Blocks](building-blocks.md) | Component overview, project structure, and module responsibilities |
| [Runtime Views](runtime-views.md) | Sequence diagrams for provisioning, connection, and reconnection flows |
| [Deployment](deployment.md) | Configuration, file layout, Terraform templates, and build targets |
| [Cross-cutting Concerns](cross-cutting.md) | Reconnection, security, config change detection, dashboard architecture |

!!! note "Template"
    This documentation follows the [arc42](https://arc42.org) architecture documentation template.
