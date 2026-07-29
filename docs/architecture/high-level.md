# High level Architecture 

The architecture uses Xray as a resilient, outbound-only TLS (port 443) transport to a central relay. SSH is layered on top to perform reverse port forwarding, allowing a server’s sshd to be securely accessed by a client through the relay, without exposing any non-HTTPS ports on the public internet.

All connectivity is egress-only from both client and server, making the design compatible with strict firewalls, NAT, and DPI-controlled environments.

```mermaid
sequenceDiagram
    participant c as Client
    participant r as Relay
    participant s as Server

    c ->> r : TLS (Xray outbound)
    s ->> r : TLS (Xray outbound)

    s ->> s : SSH over TLS (reverse port forwarding)
    note over s: Server exposes sshd to relay via SSH -R

    c ->> c : SSH over TLS (local port forwarding)
    note over c: Client maps local ports to allowed server services
    c <<-->> s : Data exchange over SSH tunnel (Bidirectional is possible)

```

### Key Properties

* **Only port 443 is publicly exposed**
* **No inbound connectivity** to client or server
* **SSH handles session security and port semantics**
* **Xray provides transport resilience and firewall traversal**
* **Relay never initiates connections**
* **Multi-tenant relay** — one relay serves many servers, each isolated behind its own path, CA (mutual TLS), and loopback port
* **Three signed roles** — every installation is a `relay`, `server`, or `client` profile; the mode is ed25519-signed as tamper-evidence


