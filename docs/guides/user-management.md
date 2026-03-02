# User Management

Each client connecting through Tunnel Whisperer needs a user account with its own credentials and port restrictions.

## Creating a User

### CLI

```bash
tw create user
```

To copy port mappings from an existing user:

```bash
tw create user --from alice
```

### Dashboard

Navigate to **Users** → **Create User**. You can optionally select an application template from the **Load from Application** dropdown to pre-fill port mappings. You can also duplicate an existing user by clicking **Duplicate** on their detail page.

### Wizard Steps

1. **Username** — alphanumeric with dashes and underscores allowed
2. **Port mappings** — define which server ports the client can access:
    - Client local port (what the client listens on)
    - Server port (the `127.0.0.1` port on the server to forward to)
    - Multiple mappings can be added sequentially
    - Optionally load from an application template or duplicate from another user
3. **Generate credentials** — creates a unique Xray UUID and ed25519 SSH key pair
4. **Update relay** — connects to the relay via a temporary Xray tunnel, adds the new UUID to the relay's Xray config
5. **Save configuration** — writes client config and keys to `users/<name>/`, appends public key to `authorized_keys`

### Generated authorized_keys Entry

```text
permitopen="127.0.0.1:5432",permitopen="127.0.0.1:8080" ssh-ed25519 AAAA... alice@tw
```

This restricts the client to forwarding only to the specified localhost ports on the server.

## Editing User Port Mappings

### CLI

```bash
tw edit user alice
```

This shows the current port mappings and prompts for new ones interactively.

### Dashboard

Click **Edit Mappings** on the user detail page. You can modify existing mappings, add new ones, or load additional mappings from an application template using the **Add from Application** dropdown.

After editing, the user's `authorized_keys` entry is updated with the new `permitopen` restrictions. A **config outdated** flag is set on the user until they re-download their config bundle.

!!! warning "Re-download required"
    After editing port mappings, the user needs to download their updated config bundle for the changes to take effect on their client.

## Listing Users

### CLI

```bash
tw list users
```

### Dashboard

The **Users** page shows all users with:

- Online status (green badge for connected users)
- Registration status (whether UUID is active on relay)
- Config outdated indicator (yellow badge when mappings changed but config not re-downloaded)
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

### CLI

```bash
# Register all users
tw apply users

# Register specific users
tw apply users alice bob
```

### Dashboard

On the **Users** page, select users and click **Apply** to batch-register them.

This:

1. Connects to the relay via temporary Xray tunnel
2. Adds each user's UUID to the relay Xray config
3. Updates each user's config with current relay settings

## Unregistering Users

To temporarily revoke relay access without deleting a user:

### CLI

```bash
tw unregister user alice
```

### Dashboard

Select users on the **Users** page and click **Unregister**.

This removes their UUID from the relay but keeps local config files intact.

## Application Templates

Application templates are reusable bundles of port mappings. Define a template once, then use it when creating or editing users to avoid entering the same ports repeatedly.

### CLI

```bash
# List templates
tw app list

# Create a template interactively
tw app create

# Edit a template
tw app edit web-app

# Delete a template
tw app delete web-app
```

### Dashboard

Navigate to **Apps** in the sidebar to manage application templates. Create, edit, and delete templates from this page.

When creating or editing a user, select an application template from the dropdown to load its port mappings.

!!! note "No retroactive changes"
    Editing an application template does not affect users that were previously created using it. Only new users or manual edits pick up the updated mappings.
