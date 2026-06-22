# Security Model

Tunnel Whisperer implements **defense-in-depth** with four independent security layers. Compromise of any single layer does not expose user data or grant unauthorized access. Every connection is outbound-only, encrypted end-to-end, and scoped to the minimum required ports per user.

---

## Four-Layer Security

### 1. Transport Layer — TLS 1.3 with mutual authentication

All traffic between clients, the relay, and servers is encrypted with **TLS 1.3** on port 443. Caddy handles TLS termination on the relay with automatic certificate provisioning via **Let's Encrypt** (ACME). To any firewall, proxy, or DPI system, Tunnel Whisperer traffic is indistinguishable from standard HTTPS.

The handshake is **mutual**: the relay is configured with `client_auth require_and_verify`, so every connection must also present an **X.509 client certificate** signed by the server's own certificate authority. Connections without a trusted certificate are rejected at the handshake — this is the relay's primary admission control. See [Relay Authentication](relay-authentication.md).

### 2. Protocol Layer — Xray VLESS + XHTTP

Inside the TLS envelope, the **VLESS protocol** tags each user with a **UUID** and splits data across standard HTTP requests via the **XHTTP transport**. The XHTTP transport makes traffic patterns resilient against restrictive network environments. The relay forwards encrypted streams without reading application data. Since admission is now decided by the certificate gate above, the UUID functions as defense-in-depth rather than the security boundary.

### 3. Session Layer — Ed25519 SSH

The innermost layer is a full **SSH session** using **Ed25519 public key authentication** (256-bit elliptic curve). SSH handles end-to-end encryption between client and server, and enforces per-user port restrictions via `permitopen` directives in `authorized_keys`. No passwords are used — there is no brute-force attack surface.

---

## Zero-Trust Relay Principle

!!! info "The relay is a blind, gated forwarder"
    The relay VM never sees plaintext application data. It stores no user credentials, no SSH keys, and no application secrets. It acts purely as an **encrypted transport passthrough** — forwarding opaque TLS streams between clients and servers. Its only knowledge is the public CA certificate(s) it has been told to trust, which it uses to admit or reject connections at the TLS handshake.

    Compromise of the relay does not expose:

    - User data or application traffic (encrypted end-to-end by SSH)
    - SSH private keys (stored only on client and server)
    - CA private keys (the relay holds only public CA certificates; signing keys never leave the server)
    - User credentials (UUID auth is per-session, keys never transit the relay)

---

## Encryption Layers Summary

| Layer              | Standard                      | Purpose                                                      |
| ------------------ | ----------------------------- | ------------------------------------------------------------ |
| TLS 1.3 + mTLS     | Industry standard + X.509     | Encrypts all data in transit; admits only certificate-bearing connections at the relay |
| VLESS + XHTTP  | Tunnel protocol               | Tags users, obfuscates traffic patterns (defense-in-depth)   |
| Ed25519 SSH        | Elliptic curve cryptography   | Authenticates tunnel endpoints, restricts per-user access    |

Each layer operates independently. The mutual-TLS gate rejects untrusted connections before any protocol byte is exchanged. Even if TLS were somehow stripped, the VLESS stream remains opaque. Even if the VLESS layer were bypassed, the SSH session provides full end-to-end encryption and authentication.

---

## Further Reading

- [Encryption](encryption.md) — detailed breakdown of each encryption layer and the end-to-end data path
- [Relay Authentication](relay-authentication.md) — the mutual-TLS gate, the per-server CA, and certificate distribution
- [Access Control](access-control.md) — user authentication, per-port authorization, and revocation procedures
