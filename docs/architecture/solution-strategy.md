# Solution Strategy

## Protocol Stack

The system layers multiple protocols to achieve secure, firewall-transparent tunneling:

```mermaid
graph TB
    subgraph "Protocol Stack (outside → inside)"
        TLS["TLS 1.3<br/><small>Caddy terminates on relay :443</small>"]
        HTTP["splitHTTP Transport<br/><small>Traffic split into standard HTTP requests</small>"]
        VLESS["VLESS Protocol<br/><small>UUID-authenticated proxy layer</small>"]
        SSH["SSH Session<br/><small>End-to-end encrypted, key-based auth</small>"]
        FWD["Port Forwarding<br/><small>direct-tcpip channels with permitopen</small>"]
        APP["Application Data<br/><small>PostgreSQL, HTTP, etc.</small>"]
    end

    TLS --> HTTP --> VLESS --> SSH --> FWD --> APP

    style TLS fill:#1565C0,color:#fff
    style HTTP fill:#1976D2,color:#fff
    style VLESS fill:#1E88E5,color:#fff
    style SSH fill:#00897B,color:#fff
    style FWD fill:#00ACC1,color:#fff
    style APP fill:#26A69A,color:#fff
```

## Technology Integration

```mermaid
graph LR
    subgraph "Infrastructure"
        TF[Terraform]
        CI[cloud-init]
        CLOUD[Cloud Provider<br/>Hetzner / DO / AWS]
    end

    subgraph "Relay VM"
        CADDY[Caddy<br/>TLS + reverse proxy]
        XRAY_R[Xray<br/>VLESS inbound]
        OSSH[OpenSSH<br/>127.0.0.1 only]
    end

    subgraph "Go Binary (tw)"
        XRAY[Xray Core<br/>in-process]
        SSHD[SSH Server<br/>x/crypto/ssh]
        GRPC[gRPC API]
        DASH[Dashboard<br/>SSE + WebSocket]
        COBRA[Cobra CLI]
    end

    TF --> CLOUD
    CI --> CLOUD
    CLOUD --> CADDY
    CADDY --> XRAY_R
    XRAY_R --> OSSH
    XRAY --> CADDY
    SSHD --> XRAY
    GRPC --> SSHD
    DASH --> GRPC
    COBRA --> GRPC

    style TF fill:#7E57C2,color:#fff
    style CI fill:#7E57C2,color:#fff
    style CLOUD fill:#5C6BC0,color:#fff
    style CADDY fill:#1565C0,color:#fff
    style XRAY_R fill:#1565C0,color:#fff
    style OSSH fill:#1565C0,color:#fff
    style XRAY fill:#00897B,color:#fff
    style SSHD fill:#00897B,color:#fff
    style GRPC fill:#00897B,color:#fff
    style DASH fill:#00897B,color:#fff
    style COBRA fill:#00897B,color:#fff
```

## Challenge-Solution Map

| Challenge | Solution | Technology |
| --------- | -------- | ---------- |
| Firewalls block non-HTTPS traffic | Encapsulate all traffic in TLS on port 443 | Xray (VLESS + splitHTTP) |
| Server and client are behind NAT | All connections are outbound-only; relay is the rendezvous point | SSH reverse port forwarding |
| Relay must never see plaintext | End-to-end encryption between client and server | SSH session layer |
| TLS certificates for the relay | Automatic issuance and renewal | Caddy (ACME / Let's Encrypt) |
| Per-user access control | Public key auth with port restrictions | SSH `authorized_keys` + `permitopen` |
| Infrastructure provisioning | Interactive wizard generates Terraform + cloud-init | Terraform (Hetzner, DigitalOcean, AWS) |
| Cross-platform operation | Single binary for both server and client | Go (Linux + Windows) |
| Dynamic user management | Re-read authorized_keys on every auth attempt | No server restart needed |
| Config change detection | SHA-256 hash comparison of config file | `crypto/sha256` |
| Real-time dashboard | SSE for progress + log streaming, WebSocket for SSH terminal | Go `net/http`, `gorilla/websocket`, xterm.js |
| Runtime log level | Dynamic `slog.LevelVar` propagates through handler chain | `log/slog` |
