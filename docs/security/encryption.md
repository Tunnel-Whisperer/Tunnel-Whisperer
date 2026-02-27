# Encryption

Tunnel Whisperer applies three nested encryption layers to every byte of application data. Each layer serves a distinct purpose and operates independently of the others.

---

## Transport Encryption (TLS 1.3)

The outermost encryption layer is **TLS 1.3** — the same standard used by online banking, healthcare portals, and every major website.

- **Caddy** handles TLS termination on the relay VM
- Certificates are automatically provisioned and renewed via **Let's Encrypt** using the ACME protocol
- All traffic is served over standard **HTTPS on port 443**
- To firewalls, proxies, and Deep Packet Inspection (DPI) systems, Tunnel Whisperer traffic is **indistinguishable from normal web browsing**

!!! info "Why TLS 1.3?"
    TLS 1.3 removes legacy cipher suites, reduces handshake round-trips, and provides forward secrecy by default. It is the minimum acceptable standard for modern encrypted communications.

---

## Tunnel Protocol (VLESS + SplitHTTP)

Inside the TLS envelope, **Xray** runs the VLESS protocol with splitHTTP transport.

- **VLESS** is a lightweight proxy protocol that authenticates each user with a unique **UUID token**. There are no shared credentials — each user has an individually issued UUID registered on the relay.
- **SplitHTTP** splits data across multiple standard HTTP requests, making the traffic pattern resilient in environments where long-lived connections are interrupted or throttled. This is critical for networks with aggressive connection timeouts or session limits.
- The relay **cannot read application data**. It receives opaque encrypted streams and forwards them to their destination. The VLESS layer handles routing; decryption happens only at the endpoints.

!!! warning "UUID is not a secret key"
    The Xray UUID functions as a transport-layer identifier, not a cryptographic secret. It provides per-user routing and revocation at the relay level. Actual data confidentiality is provided by the SSH layer below.

---

## SSH Encryption (Ed25519)

The innermost layer is a full **SSH session** providing end-to-end encryption between client and server.

- All connections use **Ed25519 public key authentication** — a 256-bit elliptic curve algorithm
- **No passwords** are used. There is no brute-force attack surface
- Each user receives an **individual Ed25519 key pair** generated during the `tw create user` wizard
- The SSH session encrypts all forwarded port traffic, ensuring that neither the relay nor any intermediate network can read application data

```
# Example: generated key pair for user "alice"
/etc/tw/config/users/alice/id_ed25519       # private key (client-side only)
/etc/tw/config/users/alice/id_ed25519.pub   # public key (added to server authorized_keys)
```

!!! info "Why Ed25519?"
    Ed25519 provides strong security with short keys (256-bit vs 3072-bit RSA for equivalent strength), fast signature verification, and resistance to timing side-channel attacks. It is the recommended key type for modern SSH deployments.

---

## End-to-End Data Path

The following diagram shows the encryption layers applied to application data as it traverses from a client application to a server service:

```
Client app                                                          Server service
    │                                                                     ▲
    ▼                                                                     │
┌──────────────┐                                                  ┌──────────────┐
│ SSH encrypt  │                                                  │ SSH decrypt  │
│ (Ed25519)    │                                                  │ (Ed25519)    │
└──────┬───────┘                                                  └──────▲───────┘
       ▼                                                                 │
┌──────────────┐                                                  ┌──────────────┐
│ Xray VLESS   │                                                  │ Xray VLESS   │
│ encode       │                                                  │ decode       │
└──────┬───────┘                                                  └──────▲───────┘
       ▼                                                                 │
┌──────────────┐         ┌──────────────┐         ┌──────────────┐┌──────────────┐
│ TLS encrypt  │────────▶│    Caddy     │────────▶│    Xray      ││ TLS encrypt  │
│ (port 443)   │  HTTPS  │ TLS terminate│  proxy  │   forward    ││ (port 443)   │
└──────────────┘         └──────────────┘         └──────────────┘└──────────────┘
    Client                     Relay                    Relay            Server
```

**Simplified linear path:**

```
Client app → SSH encryption → Xray VLESS → TLS → Caddy → Xray → SSH → Server service
```

At every point in the relay, application data remains encrypted by the SSH layer. The relay handles only the outer TLS and VLESS layers — it never has access to the plaintext content of the SSH session.

---

## Network Hardening

Beyond the encryption layers, the relay VM itself is hardened:

- The relay firewall (UFW) allows **only ports 80 and 443** — no SSH exposed to the internet
- SSH on the relay listens on **127.0.0.1 only** — accessible exclusively through the encrypted Xray tunnel
- All management operations (user provisioning, relay configuration) happen through the encrypted tunnel, never over unprotected channels
- Password authentication is **disabled** on the relay SSH daemon
