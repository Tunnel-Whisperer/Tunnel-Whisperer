# Joining a Relay

A server does not provision its own relay — it joins one owned by a relay admin. The join is a three-step, file-based handshake: the server generates a **join request**, the admin enrolls it and returns a **join response**, and the server applies that response. No live connection between server and admin is needed; the two JSON files can travel over any trusted channel (chat, email, USB stick).

```
server                          admin (relay owner)
──────                          ───────────────────
tw server join-relay <host>
        │  tw_join_<server-id>.json
        └──────────────────────►  tw relay enroll-server <file>
                                          │  tw_join_response_<server-id>.json
tw server join-relay --apply ◄────────────┘
tw server start
```

## 1. Generate the join request

On the server machine:

```bash
tw server join-relay relay.example.com
```

This sets the machine's mode to `server` (if not set yet) and generates its permanent identity: an Xray UUID, an ed25519 SSH key pair, and a per-server CA plus client certificate for the relay's mutual-TLS gate. It then writes `tw_join_<server-id>.json` to the current directory.

The request contains **public material only** — safe to transmit:

- `server_id` — derived from the hostname and UUID (e.g. `web-01-a1b2c3d4`); becomes the server's path on the relay
- `uuid` — the server's VLESS UUID
- `ca_cert_pem` — the public CA certificate the relay will trust for this server's mTLS admission (the signing key never leaves the server)
- `ssh_pubkey` — the server's SSH public key, used for the reverse tunnel to the relay

Send the file to the relay admin.

## 2. Admin enrolls the server

On the admin machine (in `relay` mode):

```bash
tw relay enroll-server tw_join_<server-id>.json
```

Enrollment is live — the relay's Caddy and Xray configs are updated and reloaded without disturbing already-enrolled servers. The admin gets back `tw_join_response_<server-id>.json` and sends it to the server.

## 3. Apply the response

Back on the server:

```bash
tw server join-relay --apply tw_join_response_<server-id>.json
```

The response contains the coordinates the admin assigned:

- `relay_host` — the relay domain
- `path` — this server's dedicated URL path on the relay (`/tw/<server-id>`)
- `remote_port` — the relay-side port allocated for this server's reverse SSH tunnel (unique per tenant)
- `ssh_user` — the SSH user for the relay tunnel account
- `mode_sig` / `mode_issuer` — the admin's signature over this machine's `server` mode (tamper-evidence; verified against the server's own identity before being stored)

Applying persists all of this to the server's config. From here on:

```bash
tw server start          # bring the tunnel up
tw server test           # expect: tunnel and shell working
```

## Re-enrolling and edge cases

**Re-running the request.** The server's identity (server-id, UUID, keys, CA) is generated once and reused — running `tw server join-relay <host>` again produces a request for the *same* identity. Use this if the original request file was lost before the admin processed it.

**After being un-enrolled.** If the admin removes the server (`tw relay un-enroll-server <server-id>`), the tunnel stops being admitted. To rejoin, repeat the full handshake — a fresh enroll on the admin side may assign a new remote port, so always apply the new response.

**Joining a second relay.** A context holds one relay membership. To join another relay without losing the current one, create a fresh context as part of the join:

```bash
tw server join-relay other-relay.example.com --new-context other-relay
```

This creates and switches to a new context named `other-relay`, preserving the current one. Switch between them with `tw config use-context <name>`.

!!! note "Mode is enforced"
    `tw server join-relay` refuses to run on a machine already set up in `relay` or `client` mode. One machine, one role.
