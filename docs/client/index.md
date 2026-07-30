# Client Role

A **client** is a machine that consumes tunnels: it connects outbound to the relay over HTTPS and gets local `localhost:<port>` listeners that transparently reach services on a remote server's private network. Which ports, and to which server, is decided entirely by the server operator — the client just imports a bundle and connects.

Everything a client needs arrives in one file: a `.twctx` context bundle issued by the server operator (`tw config export-user <name>` on their side). It contains the relay coordinates, the user's port mappings, their SSH key, and the client certificate for the relay's mutual-TLS gate. There is nothing to configure by hand.

!!! warning "One role per profile"
    Importing and activating a client bundle sets the active profile to `client`; server and relay commands then refuse to run in it. This locks the *profile*, not the machine — to act in another role on the same machine, switch to (or create) a separate [context](../global/contexts.md).

## Lifecycle

```bash
# 1. Import the bundle you received (context is auto-named after your user)
tw config import alice-tw-context.twctx --activate

# 2. Connect (foreground; or install as a service)
tw client connect

# 3. Use your mapped local ports as if the services were local
psql -h localhost -p 5432 ...
ssh -p 2201 user@127.0.0.1
```

The client reconnects automatically when the connection drops, and keeps retrying with a clear message when the server or your access isn't available. See [Connecting](connect.md) for the full flow, including `tw client listen`, `test`, `status`, and using multiple servers via contexts.
