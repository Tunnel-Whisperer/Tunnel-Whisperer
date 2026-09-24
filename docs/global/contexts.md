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
tw join relay2.example.com <code> --name relay2
```

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
stores it as a new, mode-signed context (`--switch` also activates it). The
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

### Default context names

When `--name` is omitted, the name is derived from the bundle:

| Bundle role | Default name |
| ----------- | ------------ |
| client | the username (e.g. `alice`) |
| relay | `relay-<first label of domain>` (e.g. `relay-example`) |
| server | the relay domain (sanitized) |

Names are sanitized to lowercase alphanumerics and dashes.

## Issuing Client Identities: `tw server user invite`

Server operators no longer hand out client identities as files. On the **server**:

```bash
tw server user invite alice -m 8080:80
```

This mints a one-time code carrying alice's port mappings, and blocks waiting for her to redeem it. She runs, on her own machine:

```bash
tw join relay.example.com <code>
```

Both sides show a short authentication string; she reads hers aloud, the server operator approves on an exact match, and her SSH key (generated locally — it never transits) plus a signed CSR complete the exchange. The result lands as a ready, mode-signed **client** context on her machine — activated immediately if it's a fresh install, otherwise `tw config use-context <name>` to switch to it. See [Users](../server/users.md) and [Tenants — enrolling a server](../relay/tenants.md#enrolling-a-server) for the full invite mechanics (the client and server flows share the same code+SAS design).

## Tab Completion

With [zsh completion](status-service.md#shell-completion) loaded, context selectors complete dynamically: `tw config use-context <TAB>` offers stored context **names and short IDs**, annotated with role, user, and relay. Completion reads only local state — it never dials the relay.
