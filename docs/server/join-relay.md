# Joining a Relay

A server does not provision its own relay — it joins one owned by a relay admin. The join is a **zero-file, spoken invite**: the admin mints a one-time code and reads it to you over any channel (chat, phone, whatever's convenient); you redeem it with one command, and a short authentication string confirms neither side was intercepted.

```
admin (relay owner)                    server
────────────────────                   ──────
tw relay invite
    │  mints code, waits
    │  reads code aloud ───────────────►
                                        tw join <host> <code>
    │◄──────── SPAKE2 key exchange ─────┤
    │  both sides show the SAME SAS
    │◄──── reads SAS aloud ─────────────┤
    │  confirms exact match, approves
    │  enrolls the tenant live ────────►
                                        context stored, auto-activated
                                        tw server start
```

## 1. The admin mints an invite

On the admin machine (in `relay` mode):

```bash
tw relay invite [--ttl 15m]
```

This mints a one-time code — `<tok>-NN-word-word`, drawn from a curated
BIP-39 wordlist — and blocks, waiting for you to redeem it. The admin reads
the code to you over any channel: it's a
[SPAKE2](https://en.wikipedia.org/wiki/Password-authenticated_key_agreement)
password, not a bearer credential, so someone who only overhears it can't
complete the enrollment (see the security note below).

## 2. You redeem it

On your machine:

```bash
tw join relay.example.com <code>
```

`tw join` is role-neutral — the admin's invite decided this one's a server —
and generates the new context's permanent identity locally: an Xray UUID, an
ed25519 SSH key pair, and a per-server CA plus client certificate for the
relay's mutual-TLS gate. None of this material leaves the machine except the
public halves, carried inside the encrypted PAKE channel.

Both your terminal and the admin's now show a **short authentication string**
(SAS), e.g. `7F2-A91`. **Read yours aloud to the admin** — they approve only
on an exact match.

## 3. The admin approves, enrollment runs

Once the admin confirms the read-back, their side runs the same enrollment
that always ran: register the tenant, allocate a port, rewrite the relay's
Caddyfile / Xray config / `authorized_keys`, and live-add the new tenant's
Xray inbound — no restart, no disruption to other tenants.

The result — relay host, path, allocated port, SSH user, and a signature
binding your mode to your keypair — comes back over the same encrypted
channel and is stored as a new context. If this machine had no active
profile yet, the context is **activated automatically**; otherwise:

```bash
tw config use-context <name>
```

Then bring the tunnel up:

```bash
tw server start          # foreground
tw server test           # expect: tunnel and shell working
```

## Security

- **The code is a PAKE password, not a bearer credential.** The relay only
  ever forwards ciphertext between you and the admin.
- **The SAS read-back defeats code theft and MITM.** If someone else
  redeemed the code first, they burn it — your `tw join` then fails with
  "already used", which is the tell.
- **Single-use, short TTL, burns on any attempt.** Default 15 minutes
  (`--ttl` on the admin side); the invite is consumed by the *first*
  redemption attempt, successful or not.

## Re-enrolling and edge cases

**Lost the code before redeeming it.** Invite codes aren't persisted — if it
expires or is lost, the admin just runs `tw relay invite` again.

**After being un-enrolled.** If the admin removes the server
(`tw relay un-enroll-server <server-id>`), the tunnel stops being admitted. To
rejoin, repeat the invite exchange — a fresh enrollment may assign a new
remote port.

**Joining a second relay.** A context holds one relay membership. To join
another relay without losing the current one, name the new context:

```bash
tw join other-relay.example.com <code> --name other-relay
```

This stores a new context named `other-relay`, preserving the current one.
Switch between them with `tw config use-context <name>`.

!!! note "Joining never disturbs the active context"
    `tw join` always stores its result as a **new** context — it never
    overwrites whatever is currently active. It only switches into the new
    context automatically when this machine has no active profile yet
    (a fresh install); otherwise it's created alongside the current one and
    you switch to it explicitly. This is what makes joining a second relay
    from an already-configured machine safe.
