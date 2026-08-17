# Client Role

A **client** is a machine that consumes tunnels: it connects outbound to the relay over HTTPS and gets local `localhost:<port>` listeners that transparently reach services on a remote server's private network. Which ports, and to which server, is decided entirely by the server operator — the client just redeems an invite and connects.

Everything a client needs arrives over one spoken exchange: the server operator runs `tw server user invite <name>`, reads you the resulting code over any channel, and you redeem it with `tw join`. Your SSH key and a certificate signing request are generated locally — the private halves never leave this machine — and the server signs the CSR and hands back the relay coordinates, your port mappings, and the signed client certificate for the relay's mutual-TLS gate, all over the same encrypted channel. There is nothing to configure by hand and no file to receive.

!!! warning "One role per profile"
    Redeeming an invite as a client stores a new profile with mode `client`; server and relay commands then refuse to run in it. This locks the *profile*, not the machine — to act in another role on the same machine, switch to (or create) a separate [context](../global/contexts.md).

## Lifecycle

```bash
# 1. Redeem the invite code the server operator read you
#    (context is auto-named after your user; read the SAS back to them)
tw join relay.example.com <code>

# 2. Connect (foreground; or install as a service)
tw client connect

# 3. Use your mapped local ports as if the services were local
psql -h localhost -p 5432 ...
ssh -p 2201 user@127.0.0.1
```

The client reconnects automatically when the connection drops, and keeps retrying with a clear message when the server or your access isn't available. See [Connecting](connect.md) for the full flow, including `tw client listen`, `test`, `status`, and using multiple servers via contexts.
