# User Management

Each client connecting through this server needs a user account with its own credentials and port restrictions. Users are created and managed on the server; the client receives a self-contained context bundle.

## Creating a User

```bash
# Non-interactive: name + port mappings in one command
tw server user create alice -m 8080:80 -m 5432:5432

# Copy port mappings from an existing user
tw server user create bob --from alice

# No arguments: interactive wizard
tw server user create

# Enroll the client over a one-time invite instead (see below)
tw server user create carol -m 5432:5432 --invite
```

A port mapping is `clientPort:serverPort` — the client listens on `localhost:clientPort`, the server forwards to `127.0.0.1:serverPort`. Usernames are alphanumeric with dashes and underscores.

Without `--invite`, creating a user:

1. **Generates credentials** — a unique Xray UUID and an ed25519 SSH key pair
2. **Registers the UUID on the relay** — best-effort; if the relay is unreachable at that moment you get a warning, and `tw server user apply <name>` finishes the job later
3. **Saves configuration** — writes the client config and keys to `users/<name>/` in the config directory
4. **Updates `authorized_keys`** — appends the user's public key with `permitopen` restrictions

This still leaves you with no built-in way to hand the credentials to the
client — that's what `--invite` is for (below).

In the dashboard, go to **Users → Create User** — you can pre-fill mappings from an [application template](apps.md) or duplicate an existing user; the dashboard doesn't yet offer `--invite`'s SAS confirmation, so use the CLI to actually deliver credentials.

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

**To create a user with single-session enabled from the start:**

```bash
tw server user create bob -m 8080:80 --single-session
```

You can also toggle it from the dashboard: go to the user's detail page in the **Users** section and use the single-session toggle.

## Inviting a Client

```bash
tw server user create alice -m 8080:80 -m 5432:5432 --invite
```

This mints a one-time invite code carrying alice's port mappings and blocks, waiting for her to redeem it. Read the code to her over any channel — it's a [SPAKE2](https://en.wikipedia.org/wiki/Password-authenticated_key_agreement) password, not a bearer credential. On her machine:

```bash
tw join relay.example.com <code>
```

Both terminals then show a short authentication string (SAS); she reads hers aloud and you approve only on an exact match. On approval:

- alice's machine generates its own ed25519 SSH key pair **and** a certificate signing request locally — neither private key ever transits;
- you sign the CSR with the server's CA and register her UUID on the relay, the same as a plain `tw server user create`;
- the signed certificate and her granted coordinates come back over the encrypted channel, and her machine stores a ready, mode-signed **client** context — activated immediately on a fresh machine.

There's no bundle file to send and nothing to protect in transit beyond the code itself, which the SAS check already covers.

!!! note "Single-use, short-lived"
    The invite expires after 15 minutes and burns on the first redemption attempt, successful or not — a thief who redeems it before alice does locks her out with an "already used" error, which is the tell.

## Listing Users

```bash
tw server user list
```

Shows each user's UUID and tunnel mappings. The dashboard's **Users** page additionally shows online status, relay registration, a config-outdated indicator, and tunnel counts.

## Editing Port Mappings

```bash
tw server user edit alice
```

Shows the current mappings and prompts for a replacement set. The user's `authorized_keys` entry is rewritten with the new `permitopen` restrictions immediately.

!!! warning "The client's context does not pick this up automatically"
    The client's stored context still has the old mappings — there is no push mechanism, and `tw server user create --invite` refuses to re-invite an existing name. To get a client onto a new mapping set, delete the user (`tw server user delete <name>`) and invite them again under the same name.

## Applying Users to the Relay

```bash
tw server user apply              # all users
tw server user apply alice bob    # specific users
```

Registers the users' UUIDs on the relay and refreshes each user's stored config with the current relay settings. Use it after re-provisioning or switching relays, or when `user create` warned that the relay update failed.

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
