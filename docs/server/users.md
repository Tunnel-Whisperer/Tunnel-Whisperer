# User Management

Each client connecting through this server needs a user account with its own credentials and port restrictions. A user comes to exist through exactly one flow: a one-time invite the client redeems over the network. The client generates its own keys; the server never holds a user's private material.

## Inviting a User

```bash
# Non-interactive: name + port mappings in one command
tw server user invite alice -m 8080:80 -m 5432:5432

# Copy port mappings from an existing user
tw server user invite bob --from alice

# No arguments: prompts for name and mappings first
tw server user invite

# Longer redemption window (default 15m)
tw server user invite carol -m 5432:5432 --ttl 1h
```

A port mapping is `clientPort:serverPort` — the client listens on `localhost:clientPort`, the server forwards to `127.0.0.1:serverPort`. Usernames are alphanumeric with dashes and underscores.

The command mints a one-time invite code carrying the user's port mappings and blocks, waiting for them to redeem it. Read the code to them over any channel — it's a [SPAKE2](https://en.wikipedia.org/wiki/Password-authenticated_key_agreement) password, not a bearer credential. On their machine:

```bash
tw join relay.example.com <code>
```

Both terminals then show a short authentication string (SAS); the enrollee reads theirs aloud and you approve only on an exact match. On approval:

- the client machine generates its own ed25519 SSH key pair **and** a certificate signing request locally — neither private key ever transits;
- the server signs the CSR with its CA, registers a fresh Xray UUID on the relay, and appends the public key to `authorized_keys` with `permitopen` restrictions;
- the signed certificate and the granted coordinates come back over the encrypted channel, and the client machine stores a ready, mode-signed **client** context — activated immediately on a fresh machine.

There's no bundle file to send and nothing to protect in transit beyond the code itself, which the SAS check already covers. The server keeps only public material under `users/<name>/`.

!!! note "Single-use, short-lived"
    The invite expires after the TTL (15 minutes by default) and burns on the first redemption attempt, successful or not — a thief who redeems it before the real user does locks them out with an "already used" error, which is the tell.

### Limitations to know about

- **Both sides must be online at once.** The invite is a live ceremony; there is no async pre-provisioning or credential file to hand over later.
- **One user = one enrolled device.** An existing name cannot be re-invited; a new laptop or key rotation means `tw server user delete <name>` and a fresh invite.
- **Mappings are fixed at enrollment.** There is no edit — changing a user's mappings means deleting the user and inviting them again (a "renew").
- **A relay domain change strands clients.** `tw server user apply` covers a rebuilt relay on the same domain; a new domain means re-inviting every user.
- **Enrollment is CLI-only.** The dashboard lists and manages users but cannot host the SAS ceremony.

### The authorized_keys entry

```text
permitopen="127.0.0.1:5432",permitopen="127.0.0.1:8080" ssh-ed25519 AAAA... alice@tw
```

This is the actual access-control record: the embedded SSH server only allows this key to forward to the listed localhost ports. Port forwarding to anything else is rejected.

!!! note "Revocation is live"
    The SSH server re-reads `authorized_keys` on **every** authentication attempt — no restart needed. Deleting or unregistering a user takes effect on their next connection attempt.

### Single-session enforcement

A user can be limited to one concurrent SSH connection: when enabled, a second login attempt is rejected while one is already active. The server re-checks this rule on every authentication attempt — no restart needed. Users are created with single-session enforcement off.

**To toggle single-session for an existing user:**

```bash
tw server user single-session alice on      # enable
tw server user single-session alice off     # disable
tw server user single-session alice         # show current state
```

**To enroll a user with single-session enabled from the start:**

```bash
tw server user invite bob -m 8080:80 --single-session
```

You can also toggle it from the dashboard: go to the user's detail page in the **Users** section and use the single-session toggle.

## Listing Users

```bash
tw server user list
```

Shows each user's UUID and tunnel mappings. The dashboard's **Users** page additionally shows online status, relay registration, and tunnel counts.

## Changing Port Mappings

There is no edit command. A user's mappings are fixed at enrollment — both in
their `authorized_keys` restrictions and in the client's stored context — and
the two must never drift apart. To change them, renew the user:

```bash
tw server user delete alice
tw server user invite alice -m 9090:90
```

To cut access instantly without a re-invite, just delete (or
[unregister](#unregistering-a-user)) the user — revocation is live.

## Applying Users to the Relay

```bash
tw server user apply              # all users
tw server user apply alice bob    # specific users
```

Registers the users' UUIDs on the relay and refreshes each user's stored config with the current relay settings. Use it after re-provisioning or switching relays, or when `user invite` warned that the relay update failed.

## Unregistering a User

```bash
tw server user unregister alice
```

Removes the user's UUID from the relay but keeps all local files — a temporary revocation. Restore access with `tw server user apply alice`.

## Deleting a User

```bash
tw server user delete alice
```

Removes the user's UUID from the relay, their key from `authorized_keys`, and their local files. Takes effect on the user's next connection attempt.
