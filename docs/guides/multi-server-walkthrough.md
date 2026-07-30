# Multi-Server Walkthrough: 1 Relay, 2 Servers, 2 Clients

A complete from-scratch setup: one relay, two servers enrolled on it, and two clients each reaching a server's SSH through the tunnel.

Prefer watching? The whole walkthrough, recorded live against a real topology (~3½ min, silent) — each step below also embeds just its own segment:

<video controls preload="metadata" style="width:100%; border-radius:6px;">
  <source src="../../assets/multi-server-walkthrough.mp4" type="video/mp4">
</video>

## Topology

```
                        ┌──────────────┐
   outbound 443 only    │  RELAY VM    │    outbound 443 only
 ┌─────────────────────►│ (cloud VPS,  │◄─────────────────────┐
 │                      │  80+443 open)│                      │
 │                      └──────▲───────┘                      │
 │                             │                              │
┌┴────────┐  ┌─────────┐   ┌───┴────────┐              ┌──────┴──┐  ┌─────────┐
│ server1 │  │ server2 │   │ admin      │              │ client1 │  │ client2 │
│ sshd:22 │  │ sshd:22 │   │ laptop     │              │         │  │         │
└─────────┘  └─────────┘   │ (owns the  │              └─────────┘  └─────────┘
                           │  relay)    │
                           └────────────┘
```

Five machines run `tw`: your admin laptop, server1, server2, client1, client2. The relay VM itself only runs Caddy + Xray (installed by the generated script). Nobody needs an inbound port except the relay (80/443); everyone else connects outbound over HTTPS.

!!! warning "One role per profile — run each step on the right box"
    The first role command a machine runs sets its active profile to that role (`relay`, `server`, or `client`); from then on, commands of the other two roles refuse to run in it. This locks the *profile*, not the machine — you can always hold another role in a separate [context](../global/contexts.md) — but for this walkthrough, keep it simple: one role per machine.

## Prerequisites

- A domain you control (e.g. `relay.example.com`) — you'll create one DNS A record.
- The `tw` binary installed on all 5 machines.
- Servers must be running a normal `sshd` on port 22 (that's what clients will reach).

## Step 1 — Admin: Provision the Relay

<video controls preload="metadata" style="width:100%; border-radius:6px;">
  <source src="../../assets/multi-server-step1-relay.mp4" type="video/mp4">
</video>

On your **admin laptop**:

```bash
tw relay create
```

The wizard generates keys, asks for your relay domain, cloud provider (Hetzner / DigitalOcean / AWS) and API credentials, runs Terraform, then waits for you to create the DNS A record and for TLS to come up.

**Own VPS instead?** Use the non-interactive manual path:

```bash
tw relay create --provider manual --domain relay.example.com --ip <vps-ip>
```

This emits an install script (`tw-install-relay.example.com.sh`). Copy it to the VPS, run it as root (`bash tw-install-....sh` — it prints `Setup complete`), and create the DNS A record `relay.example.com → <vps-ip>`.

Verify:

```bash
tw relay test        # DNS → HTTPS/mTLS → SSH-over-tunnel, all three must pass
```

This machine is now in **relay** mode — it's the relay's owner and the only one that can enroll servers or shell into the relay (`tw relay ssh`).

## Step 2 — Enroll server1 (join → enroll → apply)

<video controls preload="metadata" style="width:100%; border-radius:6px;">
  <source src="../../assets/multi-server-step2-server1.mp4" type="video/mp4">
</video>

**On server1:**

```bash
tw server join-relay relay.example.com
```

This writes `tw_join_<server-id>.json` in the current directory. Send that file to the admin (any trusted channel).

**On the admin laptop:**

```bash
tw relay enroll-server tw_join_<server-id>.json
```

This registers the tenant on the relay (you'll see `Caddyfile reloaded`) and writes `tw_join_response_<server-id>.json`. Send it back to server1.

**On server1:**

```bash
tw server join-relay --apply tw_join_response_<server-id>.json
tw server start          # foreground; use `sudo tw service install && sudo tw service start` to run on boot
tw server test           # expect "tunnel and shell working"
```

## Step 3 — Enroll server2

<video controls preload="metadata" style="width:100%; border-radius:6px;">
  <source src="../../assets/multi-server-step3-server2.mp4" type="video/mp4">
</video>

Repeat Step 2 exactly, on server2. Enrollment is live — server1 keeps running, no relay restart. Then confirm both tenants from the admin laptop:

```bash
tw relay get-servers     # lists server1 and server2
```

## Step 4 — Create the Client Users (on the servers)

<video controls preload="metadata" style="width:100%; border-radius:6px;">
  <source src="../../assets/multi-server-step4-users.mp4" type="video/mp4">
</video>

Each client gets a user on the server it should reach, with a port map `clientLocalPort:serverPort`. For SSH, the server port is **22**.

**On server1** (for client1):

```bash
tw server user create client1 -m 2201:22
tw server user apply client1                 # registers the user on the relay
tw config export-user client1                # writes client1-tw-context.twctx
```

**On server2** (for client2):

```bash
tw server user create client2 -m 2202:22
tw server user apply client2
tw config export-user client2
```

Send each `.twctx` bundle to its client over a trusted channel — the bundles are unprotected (no passphrase), so treat them like a private key.

## Step 5 — Connect the Clients

<video controls preload="metadata" style="width:100%; border-radius:6px;">
  <source src="../../assets/multi-server-step5-client.mp4" type="video/mp4">
</video>

**On client1:**

```bash
tw config import client1-tw-context.twctx --activate
tw client connect        # keep running; or install as a service like the servers
```

Then SSH to server1 through the tunnel:

```bash
ssh -p 2201 <your-unix-user>@127.0.0.1
```

**On client2:** same, with its own bundle and `ssh -p 2202 <user>@127.0.0.1`.

!!! note "Tunnel vs. SSH auth"
    `tw` gets you a tunnel to the server's port 22; authentication to `sshd` itself is still whatever that server's OS accounts use (your normal SSH key or password there).

## Step 6 — Verify Everything

<video controls preload="metadata" style="width:100%; border-radius:6px;">
  <source src="../../assets/multi-server-step6-verify.mp4" type="video/mp4">
</video>

```bash
# admin
tw relay test && tw relay get-servers
# each server
tw server test && tw server user list
# each client
tw client status
```

## Optional: Each Client Reaching *Both* Servers

A user bundle belongs to one server, but clients handle multiple via kubectl-style contexts:

1. On server2, also create `client1` (`tw server user create client1 -m 2211:22`, apply, export) — and mirror for client2 on server1.
2. On the client, import the second bundle too: `tw config import ... --activate`.
3. Switch with `tw config use-context <name|id>` (`tw config get-contexts` lists them). Switching reconnects — one server connection is active at a time.

## Gotchas

- Modes are enforced and signed: a client box can't run `tw server ...` commands and vice versa. If you set up a machine in the wrong mode, wipe its tw config dir and start that machine's steps over.
- The relay VM's SSH is tunnel-only after install — the admin reaches it via `tw relay ssh`. If you provisioned with `--ssh-open`, the admin key also works directly over port 22 (close it later from the dashboard's relay page).
- If you edit a user's port mappings later (`tw server user edit`), re-export and have the client re-import — the old bundle stops matching.
- To kick a server off the relay: admin runs `tw relay un-enroll-server <server-id> --yes`; to revoke a client: server runs `tw server user unregister <name>` / `delete <name>` (takes effect on their next connection attempt).
