# User Management

Each client connecting through Tunnel Whisperer needs a user account with its own credentials and port restrictions.

## Creating a User

### CLI

```bash
tw create user
```

### Dashboard

Navigate to **Users** → **Create User**.

### Wizard Steps

1. **Username** — alphanumeric with dashes and underscores allowed
2. **Port mappings** — define which server ports the client can access:
    - Client local port (what the client listens on)
    - Server port (the `127.0.0.1` port on the server to forward to)
    - Multiple mappings can be added sequentially
3. **Generate credentials** — creates a unique Xray UUID and ed25519 SSH key pair
4. **Update relay** — connects to the relay via a temporary Xray tunnel, adds the new UUID to the relay's Xray config
5. **Save configuration** — writes client config and keys to `users/<name>/`, appends public key to `authorized_keys`

### Generated authorized_keys Entry

```text
permitopen="127.0.0.1:5432",permitopen="127.0.0.1:8080" ssh-ed25519 AAAA... alice@tw
```

This restricts the client to forwarding only to the specified localhost ports on the server.

## Listing Users

### CLI

```bash
tw list users
```

### Dashboard

The **Users** page shows all users with:

- Online status (green badge for connected users)
- Registration status (whether UUID is active on relay)
- Tunnel count
- Search and pagination for large user lists

## Exporting User Config

### CLI

```bash
tw export user alice
```

This creates a zip bundle containing `config.yaml`, `id_ed25519`, and `id_ed25519.pub`. Send this to the client operator.

### Dashboard

Click the download icon next to a user on the Users page.

## Deleting a User

### CLI

```bash
tw delete user alice
```

### Dashboard

Click the delete button on the user detail page.

This removes:

- The user's UUID from the relay Xray config
- The user's public key from `authorized_keys`
- The user's local config files

!!! note "Immediate effect"
    Key removal takes effect on the client's next connection attempt — the SSH server re-reads `authorized_keys` dynamically.

## Applying Users to a New Relay

After destroying and re-provisioning a relay, existing users need their UUIDs registered on the new relay.

On the dashboard **Users** page, select users and click **Apply** to batch-register them. This:

1. Connects to the relay via temporary Xray tunnel
2. Adds each user's UUID to the relay Xray config
3. Updates each user's config with current relay settings

## Unregistering Users

To temporarily revoke relay access without deleting a user, select them and click **Unregister**. This removes their UUID from the relay but keeps local config files intact.
