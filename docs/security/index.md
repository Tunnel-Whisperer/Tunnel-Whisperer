# Security Model

Tunnel Whisperer implements **defense-in-depth** with three independent security layers. Compromise of any single layer does not expose user data or grant unauthorized access. Every connection is outbound-only, encrypted end-to-end, and scoped to the minimum required ports per user.

---

## Three-Layer Security

### 1. Transport Layer — TLS 1.3

All traffic between clients, the relay, and servers is encrypted with **TLS 1.3** on port 443. Caddy handles TLS termination on the relay with automatic certificate provisioning via **Let's Encrypt** (ACME). To any firewall, proxy, or DPI system, Tunnel Whisperer traffic is indistinguishable from standard HTTPS.

### 2. Protocol Layer — Xray VLESS + SplitHTTP

Inside the TLS envelope, the **VLESS protocol** authenticates each user with a unique **UUID** and splits data across standard HTTP requests via the **splitHTTP transport**. This layer provides per-user identity at the transport level and makes traffic patterns resilient against restrictive network environments. The relay forwards encrypted streams without reading application data.

### 3. Session Layer — Ed25519 SSH

The innermost layer is a full **SSH session** using **Ed25519 public key authentication** (256-bit elliptic curve). SSH handles end-to-end encryption between client and server, and enforces per-user port restrictions via `permitopen` directives in `authorized_keys`. No passwords are used — there is no brute-force attack surface.

---

## Zero-Trust Relay Principle

!!! info "The relay is a dumb pipe"
    The relay VM never sees plaintext application data. It stores no user credentials, no SSH keys, and no application secrets. It acts purely as an **encrypted transport passthrough** — forwarding opaque TLS streams between clients and servers.

    Compromise of the relay does not expose:

    - User data or application traffic (encrypted end-to-end by SSH)
    - SSH private keys (stored only on client and server)
    - User credentials (UUID auth is per-session, keys never transit the relay)

---

## Encryption Layers Summary

| Layer              | Standard                      | Purpose                                                      |
| ------------------ | ----------------------------- | ------------------------------------------------------------ |
| TLS 1.3            | Industry standard             | Encrypts all data in transit                                 |
| VLESS + SplitHTTP  | Tunnel protocol               | Authenticates users, obfuscates traffic patterns             |
| Ed25519 SSH        | Elliptic curve cryptography   | Authenticates tunnel endpoints, restricts per-user access    |

Each layer operates independently. Even if TLS were somehow stripped, the VLESS stream remains opaque. Even if the VLESS layer were bypassed, the SSH session provides full end-to-end encryption and authentication.

---

## Further Reading

- [Encryption](encryption.md) — detailed breakdown of each encryption layer and the end-to-end data path
- [Access Control](access-control.md) — user authentication, per-port authorization, and revocation procedures
