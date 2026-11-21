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

A port mapping is `clientPort:serverPort` — the client listens on `localhost:clientPort`, the server forwards to `127.0.0.1:serverPort`. Usernames may contain only letters, digits, `-` and `_` (`[A-Za-z0-9_-]+`). Every command and dashboard/API route that takes a user name checks this rule, so a name like `..` is refused with `user name must contain only letters, numbers, dashes, and underscores` (the dashboard API answers `400 invalid user name`).

The command mints a one-time invite code carrying the user's port mappings and blocks, waiting for them to redeem it. Read the code to them over any channel — it's a [SPAKE2](https://en.wikipedia.org/wiki/Password-authenticated_key_agreement) password, not a bearer credential. On their machine:

```bash
sudo tw join relay.example.com <code>
```

Both terminals then show a short authentication string (SAS); the enrollee reads theirs aloud and you approve only on an exact match. On approval:

- the client machine generates its own ed25519 SSH key pair **and** a certificate signing request locally — neither private key ever transits;
- the server signs the CSR with its CA, registers the client's Xray UUID on the relay, and appends the public key to `authorized_keys` with `permitopen` restrictions. The UUID must be in canonical lowercase form (`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`, as `tw join` generates it); any other form is refused and the enrollment denied, so a malformed UUID never reaches the relay's shared Xray config;
- the signed certificate and the granted coordinates come back over the encrypted channel, and the client machine stores a ready **client** context — activated immediately on a fresh machine.

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

tw rewrites the server's `authorized_keys` in full on every user change (other lines are kept, blank lines are dropped). Every key must carry a `<name>@tw` comment with a valid user name (see [Inviting a User](#inviting-a-user)). A hand-added key whose comment is not a valid user name (for example `x@host` or one containing `/`) is refused at authentication (fail closed), because the name is also used to look up the user's stored expiry.

!!! note "Revocation is live"
    The SSH server re-reads `authorized_keys` on **every** authentication attempt, and again before **every** forwarding channel it opens, so no restart is needed. A key that was removed, or whose `permitopen` no longer covers the target, cannot open new channels even on a connection that is already established. Deleting a user also closes that user's live SSH sessions (see [Deleting a User](#deleting-a-user)). The file is always replaced atomically (`0600`), so the server never reads a half-written file.

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

## Expiring a User

```bash
tw server user invite eve -m 8080:80 --expires 30d
tw server user invite eve -m 8080:80 --expires 2026-12-31
tw server user invite eve -m 8080:80 --expires 2026-12-31T17:00:00Z
```

`--expires` sets when the user's credential stops working. It accepts:

- an RFC 3339 timestamp (`2026-12-31T17:00:00Z`);
- a date, `YYYY-MM-DD`, meaning 23:59:59 UTC that day;
- a duration from now: `<n>d` for days, or any Go duration such as `12h` or `90m`.

The expiry must be more than the invite's `--ttl` plus one minute from now (the certificate is signed when the invite is redeemed, up to `--ttl` later) and no later than the server CA's own expiry. If the invite is redeemed so late that the expiry is under a minute away, the grant is refused and you mint a new invite with a later `--expires`; the signed certificate is never allowed to outlive the CA. The invite prints `Credential expires: <timestamp>`. Without `--expires` the certificate lasts 5 years.

The expiry is the `NotAfter` of the client certificate the server signs during enrollment. The server keeps a copy at `users/<name>/client.crt` (public material), and that file is the source of truth for the user's expiry. At expiry:

- the relay's mTLS check refuses the expired certificate at every new TLS handshake, so a new connection never reaches the server;
- the server's SSH layer refuses an expired user's authentication and every new channel (log line `expired user refused`). This is what stops a client that is still connected, which can otherwise re-authenticate without a new TLS handshake;
- the running server daemon disconnects the user's live SSH sessions at the moment of expiry (log line `user credential expired, sessions disconnected`). The daemon rescans users every 60 seconds, so a user invited from the CLI is picked up within a minute.

Nothing is deleted: the user stays in `tw server user list` as `EXPIRED`, and its UUID stays registered on the relay until you delete it.

!!! note "No extend or renew"
    An expiry cannot be changed. To continue, delete the user and invite it again; inviting an expired name is refused with `has expired; delete it first`.

- Users invited before this feature have no stored certificate and never expire.
- A `client.crt` that cannot be read or parsed is treated as no expiry (fails open), and a warning is logged.
- Only client users expire. A server (tenant) has no expiry.

## Listing Users

```bash
tw server user list
```

Shows each user's UUID, credential expiry and tunnel mappings. An `Expires:` line appears only for users with a stored certificate: the expiry time, `EXPIRED` once it has passed, and an `(in Nd)` hint within 7 days (`(in <1d)` under a day). UUIDs are shown, and sent to the relay, in lowercase, even if a user's stored config has them in uppercase. The dashboard's **Users** page additionally shows online status, relay registration, tunnel counts, and an **Expires** column (an `expired` badge once the credential has expired).

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

Registers the users' UUIDs on the relay and refreshes each user's stored config with the current relay settings. Use it after re-provisioning or switching relays, or when `user invite` warned that the relay update failed. A UUID that is not in canonical lowercase form is refused (`refusing to register non-canonical UUID ...`).

An enrolled server has no shell on the relay. It adds and removes its users' UUIDs through the relay's tenant request handler, which only touches this server's own inbound (see [Tenants](../relay/tenants.md#tenant-accounts-on-the-relay)). If the relay has not been migrated yet, this fails with a hint for the relay admin to run `tw relay sync-tenants`.

## Unregistering a User

```bash
tw server user unregister alice
```

Removes the user's UUID from the relay but keeps all local files — a temporary revocation. Restore access with `tw server user apply alice`. On an enrolled server the relay's tenant handler also removes the UUID from the running Xray through its API, so new relay connections with it are refused at once. If that live update fails, `tw` warns you, and the removal takes effect at the relay's next Xray restart.

## Deleting a User

```bash
tw server user delete alice
```

Removes the user's UUID from the relay, their key from `authorized_keys`, and their local files, in that order. Then it closes every SSH session the user still has open on the running server, so access ends immediately, not on the next connection attempt. When the daemon is running, the CLI deletes through it. Even if a session is not closed (for example because the deleting process does not run the SSH server), the per-channel `authorized_keys` check stops it from opening new forwards.

If removing the key from `authorized_keys` fails, the command fails and leaves the user's files in place, so you can simply run it again. A user whose public key file is already gone is removed from `authorized_keys` by its `<name>@tw` comment. A failure to remove the UUID from the relay is only logged as a warning.
