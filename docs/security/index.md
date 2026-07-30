# Security Model

Tunnel Whisperer implements **defense-in-depth** with three independent security layers. Compromise of any single layer does not expose user data or grant unauthorized access. Every connection is outbound-only, encrypted end-to-end, and scoped to the minimum required ports per user.

---

## Three-Layer Security

### 1. Transport Layer — TLS 1.3 with mutual authentication

All traffic between clients, the relay, and servers is encrypted with **TLS 1.3** on port 443 (the relay's Caddyfile pins `protocols tls1.3`). Caddy handles TLS termination on the relay with automatic certificate provisioning via **Let's Encrypt** (ACME). To any firewall, proxy, or DPI system, Tunnel Whisperer traffic is indistinguishable from standard HTTPS.

The handshake is **mutual**: the relay is configured with `client_auth require_and_verify`, so every connection must also present an **X.509 client certificate** signed by an enrolled server's own certificate authority (ECDSA P-256, CN = the server-id). Connections without a trusted certificate are rejected at the handshake — this is the relay's primary admission control. On a multi-tenant relay, the certificate subject also routes the connection: each server's traffic only reaches that server's own upstream. See [Relay Authentication](relay-authentication.md).

### 2. Protocol Layer — Xray VLESS + XHTTP

Inside the TLS envelope, the **VLESS protocol** tags each user with a **UUID** and carries data over the **XHTTP transport** (HTTP semantics end to end), which keeps traffic patterns resilient in restrictive network environments. The relay forwards encrypted streams without reading application data, and its per-tenant Xray routing only permits loopback connections to SSH and the tenant's own allocated port — everything else is blackholed. Since admission is decided by the certificate gate above, the UUID functions as defense-in-depth rather than the security boundary.

### 3. Session Layer — Ed25519 SSH

The innermost layer is a full **SSH session** using **Ed25519 public key authentication** (256-bit elliptic curve). SSH handles end-to-end encryption between client and server, and enforces per-user port restrictions via `permitopen` directives in the server's `authorized_keys`, which is re-read on every authentication attempt (live revocation). No passwords are used — there is no brute-force attack surface.

---

## Zero-Trust Relay Principle

!!! info "The relay is a blind, gated forwarder"
    The relay VM never sees plaintext application data. It stores no user credentials and no CA signing keys. It acts purely as an **encrypted transport passthrough** — forwarding opaque TLS streams between clients and servers. Its only trust material is the public CA certificate of each enrolled server (used to admit or reject connections at the TLS handshake) and the SSH **public** keys in its `authorized_keys`.

    Compromise of the relay does not expose:

    - User data or application traffic (encrypted end-to-end by SSH)
    - SSH private keys (stored only on clients, servers, and the admin machine)
    - CA private keys (the relay holds only public CA certificates; signing keys never leave each server)
    - User credentials (UUID auth is per-session, keys never transit the relay)

### Relay SSH is locked down

SSH on the relay is management plumbing, not a user-facing surface:

- **Admin key** — the relay owner's `id_ed25519.pub`, pinned `from="127.0.0.1"` so it only authenticates for connections arriving through the encrypted tunnel. Exception: a relay provisioned with `--ssh-open` leaves the admin key unpinned so it also works over the deliberately opened public port 22. Only the admin can open a shell.
- **Tenant (server) keys** — always pinned `from="127.0.0.1"` and locked with `restrict,port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:<own port>"`: forwarding only, no shell/exec, reverse-forwarding limited to the tenant's own allocated port, and local forwarding pinned to a dead sentinel so a tenant cannot reach the relay's loopback services (e.g. the Xray gRPC API).
- Unless `--ssh-open` was chosen, the firewall allows only ports 80/443 and `sshd` listens on `127.0.0.1` only. Password authentication is disabled either way.

---

## Signed mode (tamper-evidence, not a boundary)

Each profile's `mode` field (`relay`/`server`/`client`) carries an ed25519 signature (`mode_auth`) over the mode and the profile's identity key — issued by the relay at enrollment (servers), by the server at user export (clients), or self-signed (the relay itself). A hand-edited mode fails verification and the role's commands refuse to run.

This is **tamper-evidence, not the security boundary**. A user who controls their machine can always rebuild a profile; doing so gains no power, because the real role boundary lives on the relay — the `authorized_keys` restrictions above and the mTLS trust chain — which key possession, not a config field, decides.

---

## Encryption Layers Summary

| Layer              | Standard                      | Purpose                                                      |
| ------------------ | ----------------------------- | ------------------------------------------------------------ |
| TLS 1.3 + mTLS     | Industry standard + X.509     | Encrypts all data in transit; admits only certificate-bearing connections at the relay and routes them per server |
| VLESS + XHTTP  | Tunnel protocol               | Tags users, keeps traffic patterns HTTPS-shaped (defense-in-depth)   |
| Ed25519 SSH        | Elliptic curve cryptography   | Authenticates tunnel endpoints, restricts per-user access    |

Each layer operates independently. The mutual-TLS gate rejects untrusted connections before any protocol byte is exchanged. Even if TLS were somehow stripped, the VLESS stream remains opaque. Even if the VLESS layer were bypassed, the SSH session provides full end-to-end encryption and authentication.

---

## Further Reading

- [Encryption](encryption.md) — detailed breakdown of each encryption layer and the end-to-end data path
- [Relay Authentication](relay-authentication.md) — the mutual-TLS gate, the per-server CA, and certificate distribution
- [Access Control](access-control.md) — user authentication, per-port authorization, and revocation procedures
