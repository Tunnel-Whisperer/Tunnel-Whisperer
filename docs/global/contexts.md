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

`new-context` preserves the current context — it is sealed to disk and stays in the list, ready to switch back to. A typical use is joining a second relay from an already-configured server:

```bash
tw server join-relay relay2.example.com --new-context relay2
```

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
```

The relay's bundle is also written automatically at the end of `tw relay create` — it is the relay's only backup (CA keypair, relay SSH key, metadata). There is no recovery if it is lost.

### Import

```bash
tw config import <bundle.twctx>
tw config import <bundle.twctx> --activate      # switch to it immediately (applies its mode)
tw config import <bundle.twctx> --name work     # store under a custom name
tw config import <bundle.twctx> --force         # replace an existing same-name context without prompting
```

If a context of the same name already exists, `tw` asks before replacing it (or keeps it, if you decline). Re-importing a bundle for the **active** context refreshes the live profile in place.

### Default context names

When `--name` is omitted, the name is derived from the bundle:

| Bundle role | Default name |
| ----------- | ------------ |
| client | the username (e.g. `alice`) |
| relay | `relay-<first label of domain>` (e.g. `relay-example`) |
| server | the relay domain (sanitized) |

Names are sanitized to lowercase alphanumerics and dashes.

## Issuing User Bundles: `export-user`

Server operators hand out client identities as context bundles too. On the **server**:

```bash
tw config export-user alice
# → alice-tw-context.twctx
```

This packages the user `alice` as a ready-to-import **client** context (keys, client certificate, port mappings). Send the file to the client, who runs:

```bash
tw config import alice-tw-context.twctx --activate
tw client connect
```

`export-user` is server-mode only. It works whether or not the server daemon is running.

## Tab Completion

With [zsh completion](status-service.md#shell-completion) loaded, context selectors complete dynamically: `tw config use-context <TAB>` offers stored context **names and short IDs**, annotated with role, user, and relay. Completion reads only local state — it never dials the relay.
