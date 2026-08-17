# Server Role

A **server** is a machine on a private network that exposes selected local services (databases, SSH, web apps — anything listening on `127.0.0.1`) to remote clients through a relay. The server never opens an inbound port: it dials *out* to the relay over HTTPS (port 443) and publishes its embedded SSH endpoint there through a reverse tunnel.

In the three-role model, a server is a **tenant** of a relay. The relay is owned and operated by its admin (the machine in `relay` mode); a server joins it through an explicit enrollment handshake and gets its own path (`/tw/<server-id>`) and port allocation on the relay. Multiple servers can share one relay, each isolated under its own path and mutual-TLS identity.

!!! warning "One role per profile"
    The first server command sets the active profile to `server`, and the mode is cryptographically signed; relay and client commands then refuse to run in it. This locks the *profile*, not the machine — another role can live in its own [context](../global/contexts.md) — but run server commands only on the machine that should *be* the server.

## Lifecycle

Setting up a server is three steps, in order:

1. **[Join a relay](join-relay.md)** — get a one-time invite code from the relay admin (`tw relay invite`), redeem it:

    ```bash
    tw join relay.example.com <code>
    ```

2. **[Create users](users.md)** — one per client, each restricted to specific ports:

    ```bash
    tw server user invite alice -m 5432:5432
    ```

3. **[Start the server](running.md)** — run the daemon (or install it as a service):

    ```bash
    tw server start
    tw server test        # verify: tunnel and shell working
    ```

## What runs on a server

`tw server start` runs everything in one process: an embedded SSH server, an Xray client tunnel to the relay, the reverse tunnel that publishes SSH on the relay, a gRPC API, and the web dashboard. See [Running the Server](running.md) for details.

## In this section

- [Joining a Relay](join-relay.md) — the invite → SAS approval handshake
- [User Management](users.md) — create, invite, apply, edit, revoke users
- [Application Templates](apps.md) — reusable port-mapping bundles
- [Running the Server](running.md) — `start`, `test`, `status`, running as a service
