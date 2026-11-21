# Contexts

Contexts are `tw`'s kubectl-style profiles: each one is a complete identity — role, keys, relay, users — and `tw config use-context` switches between them. One machine can be a client of two different relays, or a relay admin *and* a server, without the identities ever mixing.

## Listing Contexts

```bash
tw config get-contexts
```

```text
CURRENT   NAME              ID         ROLE     USER      RELAY
*         relay-example     3f2a9c1e   relay              relay.example.com
          alice             81d0b44f   client   alice     relay.example.com
          backup-server     c07e55aa   server             relay.backup.net
```

- **CURRENT** — `*` marks the active context.
- **ID** — the short ID: the first 8 hex characters of the context's Xray UUID (dashes stripped). Empty until the context is configured.
- **ROLE / USER / RELAY** — the context's mode, its username (client contexts), and its relay domain.

```bash
tw config current-context    # print just the active context's name
tw config view               # print the active context's config.yaml
tw config view --as-json     # same config as indented JSON (for scripts/jq)
```

## IDs as Selectors

Everywhere a command takes a `<name|id>`, the short ID works too:

```bash
tw config use-context alice
tw config use-context 81d0b44f     # same context
```

## Switching, Creating, Renaming, Deleting

```bash
tw config use-context <name|id>       # switch (re-seals the current context, reconnects)
tw config new-context <name>          # create a fresh empty context and switch to it
tw config rename-context <old|id> <new>
tw config delete-context <name|id>
```

Deleting a context also removes its Terraform provider cache (`cache/terraform/<context-id>/`, see [File Layout](../reference/file-layout.md#the-cache-directory)).

`new-context` preserves the current context — it is sealed to disk and stays in the list, ready to switch back to. A typical use is joining a second relay from an already-configured server: `tw join` always stores its result as a new context (auto-named from the relay host or username, or pass `--name` to choose), so joining a second relay doesn't disturb the first:

```bash
sudo tw join relay2.example.com <code> --name relay2
```

### Context names

A context name is used as a file name (`contexts/<name>.twctx`), so it must
be a single path component: not empty, not `.` or `..`, and without `/`,
`\` or NUL. `new-context`, `rename-context`, `use-context`, `export`,
`import --name` and `tw join --name` all refuse anything else with `invalid
context name "<name>": must be a single path component without / or \`.
Derived default names are always valid. For a client join, the name derived
from the issuer-supplied username is sanitized first, so an invite cannot
pick a path.

Older versions could store such a name in the index (e.g. from a username
like `evil/../relay-admin`). Switching away from it then fails with `the
current context cannot be saved`. Remove the entry with:

```bash
tw config delete-context '<invalid-name>'
```

For an invalid name this only drops the index entry. It never deletes a
file and never runs the full reset, even for the last context. If it was the
current context, the live profile stays in place, and the next command
adopts it under a fresh valid name (the default derived name, with `-2`,
`-3`, … appended if that is taken). Check the result with `tw config
get-contexts` and rename it if you like.

## One Person, All Roles

Running relay admin and server (or client) as one person on one machine is a
first-class setup — each role simply lives in its own context. To make the
relay admin's own machine a server, don't do the join-file handshake with
yourself; from the relay context run:

```bash
tw relay add-server            # creates context server-<relay label>, e.g. server-relay2
tw config use-context server-relay2
tw server start
```

One command enrolls a freshly generated server identity on the relay and
stores it as a new context (`--switch` also activates it). The
relay context is untouched, and the new context gets its own dashboard/API
ports so both daemons can run side by side. See
[Tenant Management](../relay/tenants.md#same-machine-self-enrollment).

!!! warning "Deleting the last context is a full reset"
    If you delete the only remaining (active) context, `tw` removes **all** configuration from the machine — identity, keys, relay data — after an explicit confirmation. It refuses if the `tw` service is running (stop it first with `tw service stop`).

!!! note "A running service doesn't follow the switch"
    A `tw` service keeps serving the config it loaded at startup. After `tw config use-context`, both the switch command and `tw status` warn if the running service still serves the old context — restart it to apply (`tw service stop && tw service start`; Windows: `Restart-Service tw`).

## Bundles: Export & Import

A context travels as a single portable file, `tw_<name>.twctx` (dots/colons/slashes in the name become dashes). Bundles carry **no passphrase** — importing never prompts.

!!! danger "Bundles are unprotected"
    A `.twctx` file *is* the identity: whoever holds it can use it. Treat it like a private key and transfer it only over a trusted channel.

### Export

```bash
tw config export              # export the active context → tw_<name>.twctx
tw config export <name|id>    # export a stored context
tw config export -o /path/to/dir           # write into a directory as tw_<name>.twctx
tw config export <name|id> --output custom.twctx  # write to an exact file path
```

Without `-o`, the bundle is written to the current directory. With `--output`/`-o`, a path that is an existing directory (or ends in a slash) receives `tw_<name>.twctx` inside it; any other path is used as the exact file name. Parent directories are not created.

The relay's bundle is also written automatically at the end of `tw relay create` (always to the directory you ran it from) — it is the relay's only backup (CA keypair, relay SSH key, metadata). There is no recovery if it is lost.

A bundle is **machine- and OS-independent**: import it on another host — different hostname, Linux or Windows — and it keeps its identity. The server-id is stored in the bundle's `config.yaml` (`xray.server_id`) rather than re-derived from the hostname (see [Relay Authentication](../security/relay-authentication.md#stored-server-id)), and certificate paths are derived from the config dir at runtime. Terraform's provider cache lives outside the profile, under `cache/terraform/<context-id>/`, and is never bundled — the importing machine re-creates it on its next Terraform call — so even a cloud relay's bundle is a few hundred KB at most. The exact contents are listed in [File Layout → Context bundles](../reference/file-layout.md#context-bundles-twctx).

### Import

```bash
tw config import <bundle.twctx>
tw config import <bundle.twctx> --activate      # switch to it immediately (applies its mode)
tw config import <bundle.twctx> --name work     # store under a custom name
tw config import <bundle.twctx> --force         # replace an existing same-name context without prompting
```

If a context of the same name already exists, `tw` asks before replacing it. Declining keeps the existing context but the command exits non-zero, so a script can tell nothing was imported; when stdin is closed (a non-interactive caller) the collision is an error outright — pass `--force` to replace without asking. Re-importing a bundle for the **active** context refreshes the live profile in place.

### What activating a bundle does

Activating a context (`use-context`, `import --activate`, the reapply after
re-importing the active context, or `tw join` on a fresh machine) unseals its
bundle into the config dir:

- **The bundle replaces the live profile.** The profile is the flat files
  `config.yaml`, `ca.crt`, `ca.key`, `client.crt`, `client.key`,
  `id_ed25519(.pub)` and `relay_host_ed25519(.pub)`, plus the `relay/`,
  `users/` and `servers/` directories. Afterwards it holds exactly what the
  bundle carries. A file the bundle does not contain is removed, so nothing
  from one context leaks into the next (for example a `servers/<id>.json`
  that would otherwise be resealed into the context you switch back to).
- **Only profile entries are accepted.** A bundle that contains anything
  else (e.g. `authorized_keys`, `ssh_host_ed25519_key`, `api.token`,
  `dashboard.token`, the contexts store or a path outside the config dir) is
  rejected with `profile bundle contains unexpected entry "<name>"`, before
  anything is written. Terraform cache entries in bundles sealed by older
  versions are skipped silently.
- **Restrictive modes.** Directories are created `0700`. Files are written
  `0600`, except `*.crt` and `*.pub` outside `relay/`, which are `0644`.
  Everything under `relay/` (cloud-init with the relay host key,
  `terraform.tfvars` with the cloud API token, the Terraform state) is
  `0600`, and existing directories are tightened as well.
- **All or nothing.** The new profile is staged first. If moving it into
  place fails partway, the moves already done are rolled back.

The relay registry (`servers/`) inside a bundle is checked again when it is
read: an entry that fails the [join-request checks](../server/join-relay.md#security)
is skipped with an error in the log and never rendered onto the relay. `tw
relay un-enroll-server <id>` still removes it.

### Default context names

When `--name` is omitted, the name is derived from the bundle:

| Bundle role | Default name |
| ----------- | ------------ |
| client | the username (e.g. `alice`) |
| relay | `relay-<first label of domain>` (e.g. `relay-example`) |
| server | the relay domain (sanitized) |

Derived names are sanitized to lowercase alphanumerics and dashes. A name
given with `--name` is used as is, but must be a valid
[context name](#context-names).

## Issuing Client Identities: `tw server user invite`

Server operators no longer hand out client identities as files. On the **server**:

```bash
tw server user invite alice -m 8080:80
```

This mints a one-time code carrying alice's port mappings, and blocks waiting for her to redeem it. She runs, on her own machine:

```bash
sudo tw join relay.example.com <code>
```

Both sides show a short authentication string; she reads hers aloud, the server operator approves on an exact match, and her SSH key (generated locally — it never transits) plus a signed CSR complete the exchange. The result lands as a ready **client** context on her machine — activated immediately if it's a fresh install, otherwise `tw config use-context <name>` to switch to it. See [Users](../server/users.md) and [Tenants — enrolling a server](../relay/tenants.md#enrolling-a-server) for the full invite mechanics (the client and server flows share the same code+SAS design).

## Tab Completion

With [zsh completion](status-service.md#shell-completion) loaded, context selectors complete dynamically: `tw config use-context <TAB>` offers stored context **names and short IDs**, annotated with role, user, and relay. Completion reads only local state — it never dials the relay.
