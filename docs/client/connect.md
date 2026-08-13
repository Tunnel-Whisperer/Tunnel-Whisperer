# Connecting

This page covers the full client workflow: redeem the invite, connect, use your local ports — plus binding, testing, status, and reconnection behavior.

## 1. Redeem the Invite

The server operator runs `tw server user create <name> --invite` and reads you the resulting one-time code over any channel. Redeem it with:

```bash
tw join relay.example.com <code> [--name alice]
```

Both terminals show a short authentication string (SAS) once the code exchange completes — **read yours aloud to the server operator**; they approve only on an exact match, which is what makes overhearing the code alone useless to an eavesdropper. On approval:

- your machine generates an ed25519 SSH key pair **and** a certificate signing request locally — neither private key ever leaves it;
- the server signs the CSR and sends back the relay coordinates, your port mappings, and the signed client certificate for the relay's mutual-TLS gate, all over the encrypted PAKE channel;
- the result is stored as a new context, auto-named after your user (`alice`) unless you pass `--name`, and activated immediately if this machine has no active profile yet — otherwise `tw config use-context alice`.

The client certificate is the same for every user of that server; your individual access is enforced by your per-user SSH key.

!!! note "Moving your own identity between machines"
    `tw join` is for *receiving* a new identity from a server operator. To move a context you already own to another machine of yours, use `tw config export` / `tw config import` — a `.twctx` bundle carries no passphrase, so treat it like a private key in transit.

## 2. Connect

```bash
tw client connect
```

This runs in the foreground (Ctrl-C disconnects) and starts:

1. **Xray client tunnel** to the relay — VLESS + XHTTP over TLS on port 443, admitted by the relay's mTLS gate
2. **SSH connection** through Xray to the server (public-key auth with your user's key)
3. **Local port listeners** — one per mapping in your bundle

## 3. Use Your Local Ports

Each mapping gives you a `localhost` port that reaches the corresponding service on the server. For example, with PostgreSQL mapped to 5432 and the server's SSH mapped to 2201:

```bash
psql -h localhost -p 5432 -U myuser mydb
ssh -p 2201 <your-unix-user>@127.0.0.1
```

The tunnel is transparent to the application.

!!! note "Tunnel vs. application auth"
    `tw` gets you the port; authenticating to the service behind it (database password, `sshd` login, ...) is unchanged.

## Listen Address

Forwarded ports bind to `127.0.0.1` by default. To expose them on other interfaces — required when running `tw` in a container that publishes ports to the host:

```bash
tw client listen              # show the current address
tw client listen 0.0.0.0      # bind on all interfaces
tw client listen 127.0.0.1    # restore the default (local only)
```

Takes effect on the next reconnect.

## Local Port Conflicts & Overrides

The `local_port` values granted to you were chosen by the server admin — they
may clash with something already running on *your* machine. The local port is
purely your machine's business (access control is enforced on the server
port), so you can remap it freely.

`tw join` already preflights every granted port during enrollment and prompts
for a replacement on the spot if one's busy, so a fresh join usually never
hits this. If a port is taken later — something else grabbed it since, or you
freed and re-lost it — `tw client connect` fails fast with:

```text
local port 8080 (→ server port 15432) is already in use — override it with
'tw client set-port 15432 <port>' or 'tw client connect --map <port>:15432'
```

**Persistent override** — remap the tunnel identified by its *server* port:

```bash
tw client set-port 15432 4000     # server port 15432 now binds on localhost:4000
tw client set-port                # list tunnels: default, override, effective
tw client set-port 15432 --clear  # back to the admin default
```

Changes take effect on the next reconnect.

**One-shot override** — for a single run, without persisting anything:

```bash
tw client connect --map 4000:15432          # <local_port>:<server_port>, like ssh -L
tw client connect --map 4000:15432 --map 4001:15433
```

`--map` wins over a persisted override for that run only; a reconnect from the
dashboard or service drops it.

!!! note "Re-importing a bundle resets overrides"
    `tw config import` replaces the whole context, including
    `port_overrides` — this matters if you re-import your own exported
    backup bundle onto a different machine. Re-run `tw client set-port` for
    any ports you had remapped.

## Test and Status

```bash
tw client test        # relay connectivity: DNS → HTTPS/mTLS → tunnel
tw client status      # active context, relay info, client component health
```

`tw client status` shows the context header (name, mode, user, relay, config path), the relay block, and — while connected — the client's Xray and tunnel health with any tunnel error. If a running `tw` service was started under a different context than the active one, status warns about the mismatch.

## Reconnection

The client reconnects automatically with gradual backoff: 2 s for the first 8 attempts, then 4 s, 8 s, and 16 s (4 attempts each), capping at 30 s. A successful connection resets the backoff, and a keepalive failure or dropped forward triggers an immediate reconnect cycle.

If the tunnel comes up but the server can't be reached, the client keeps retrying and tells you why:

```text
cannot reach the server through the relay — either the server's tunnel is down
(not running, restarting, or un-enrolled) or this user's access is not active
on the relay (not applied yet, or revoked); retrying
```

If this persists, ask the server operator to check `tw server status` and `tw server user apply <your-user>`.

## Multiple Servers

Each context belongs to one server, but a client can hold several as kubectl-style contexts — `tw join` never disturbs the active context, so joining a second server (whether it's a second tenant on the same relay or a different relay entirely) is safe from an already-configured client:

```bash
tw join relay.example.com <code2> --name bob   # a second server's invite
tw config get-contexts                         # list stored contexts
tw config use-context bob                      # switch (reconnects)
```

One server connection is active at a time; switching contexts reconnects to the newly selected one.

## Run as a Service

To keep the client connected in the background and auto-connect on boot:

=== "Linux"

    ```bash
    sudo tw service install
    sudo tw service start
    ```

=== "Windows"

    ```powershell
    tw.exe service install
    tw.exe service start
    ```
