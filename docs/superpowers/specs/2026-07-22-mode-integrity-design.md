# Mode integrity: rename admin→relay, signed mode, strict role gating

Date: 2026-07-22
Status: approved

## Problem

Four related defects in how `tw` represents and enforces a profile's role:

1. The canonical mode string is still `admin` internally, while every
   user-facing command group is now `tw relay`. The split is confusing and
   the memory/rename history explicitly deferred unifying it.
2. A profile's `mode` is a plain, hand-editable YAML field. Editing it to a
   higher-privilege role silently unlocks that role's CLI commands. There is
   no tamper-evidence.
3. Several role-acting commands (`tw server join-relay`, `tw client listen`,
   `tw proxy set/clear`) have no mode gate at all, so a profile in one mode
   can invoke another role's operation.
4. `tw config export-user`'s short description calls the bundle "encrypted",
   implying confidentiality it does not provide — the bundle is sealed with
   an empty passphrase (openable by anyone), as its own `Long` text admits.

## Security model (read this before the mechanics)

The **true role boundary is cryptographic and already exists**, keyed to
which keypair a profile holds — nothing about mode needs to live on the
relay:

- **Relay operations** go through `withRelaySSH` (`internal/ops/user.go`),
  which dials the relay with the profile's own `id_ed25519` and runs
  `sudo …` commands. The relay's `authorized_keys`
  (`renderRelayAuthorizedKeys`, `internal/ops/enroll.go`) grants the
  relay/admin key a shell (no `restrict`) and every enrolled **server** key
  `restrict,port-forwarding,…` — forward-only, **no exec**. A server's
  keypair therefore *physically cannot* run a reconfiguration command on the
  relay; sshd rejects exec on a `restrict` key. Relay reconfiguration
  requires the relay's own admin keypair, generated at provision time and
  never shared.
- **client→server / server→client** escalation gains nothing: issuing users
  needs a server CA the relay was told to trust for a server path; connecting
  as a user needs a client cert signed by a server CA the relay admits at its
  mTLS gate. Forging a local `mode` produces neither.

The **signed mode** in this spec is a **local tamper-evidence layer on top**:
it makes `tw` refuse a role's commands when the config `mode` was edited,
failing fast with a clear message instead of dialing out to a confusing
sshd/mTLS denial. It is explicitly **not** a hard wall — a user who fully
controls their machine can regenerate a whole profile, which is inherently
unpreventable client-side and gains no real power, because the power lives in
the keys, not the mode field. The spec documents this framing in the code so
no one mistakes it for a security boundary it isn't.

## Part 1 — Rename `admin` → `relay` (with migration)

`relay` becomes the canonical mode string. `admin` becomes a **read-time
alias**, migrated on load so existing installs (including the live field
relay) keep working with zero manual steps.

- `internal/config`: `ValidMode` accepts `server|client|relay`. A new
  `CanonicalMode(m string) string` maps `admin`→`relay`, else returns `m`.
  `config.Load()` applies it to `cfg.Mode` after unmarshal; if the value
  changed, it is **rewritten to disk once** (best-effort; a read-only dir is
  not fatal, the in-memory value is still canonical).
- Context metadata: `ContextMeta.Role` and `DefaultContextName`'s
  `role == "admin"` branch canonicalize the same way; the lazy context
  backfill (`ListContexts`) rewrites `admin`→`relay` when it next touches an
  entry. Default relay context name becomes `relay-<first-label>` (was
  `admin-…`); existing stored context *names* are left as-is (user renames
  manually if desired).
- All internal literals: `requireMode("admin")`→`requireMode("relay")`
  (8 call sites), `cfg.Mode == "admin"` and the two `cfg.Mode = "admin"`
  provisioning stamps in `internal/ops/relay.go`, `Role: "admin"` in
  `enroll.go`, the caddy `Role` comment, `handlers_pages.go`'s
  `mode == "admin"` / `renderPage("admin", …)`, and the dashboard template
  file `pages/admin.html` (renamed to `pages/relay.html`, with its `nav.html`
  / `index.html` / `setup.html` references updated).
- Docs/help strings referencing the "admin" mode/role are updated to
  "relay"; the *word* "admin" survives only where it means a human
  administrator, not the mode.

Verification that migration works: an e2e/unit fixture with `mode: admin`
loads as `relay` and (where writable) is rewritten.

## Part 2 — Signed mode (tamper-evidence)

### What is signed

A canonical payload string, versioned:

```
tw-mode-v1\n<mode>\n<identity>
```

- `<mode>` is `relay` | `server` | `client`.
- `<identity>` binds the token to *this* profile so it cannot be copied to a
  different keypair: the profile's own SSH public key in authorized-keys form
  (`id_ed25519.pub` contents, trimmed). For a client bundle it is the
  client's own `id_ed25519.pub`.

### Who signs (the issuer chain)

| Profile | Signed by | When |
|---|---|---|
| relay  | itself (its own `id_ed25519`)     | `tw relay create` / manual-install generate |
| server | the **relay** (relay `id_ed25519`) | inside the join **response**, at enroll |
| client | the **server** (server `id_ed25519`) | inside the user bundle, at issue |

Ed25519 detached signatures, verified with `golang.org/x/crypto/ssh` signers
already used for these keypairs. New package `internal/ops/modeauth`
(pure: `Sign(privPEM, mode, identity) (sigB64, issuerPubB64)`,
`Verify(mode, identity, sigB64, issuerPubB64) error`, and the canonical
payload builder) with unit tests — no I/O, so it is TDD-friendly and isolated.

### Where the token lives

A new config block, persisted in `config.yaml`:

```yaml
mode_auth:
  sig: <base64 ed25519 signature over the canonical payload>
  issuer: <base64 ed25519 public key of the signer>
```

For a **server**, the join response carries `mode_sig` + `mode_issuer`
(new fields on `JoinResponse`, version stays 1 — additive, older/newer
tolerate missing); `ApplyJoinResponse` writes them into `mode_auth`.
For a **client**, `GetUserConfigBundle` injects `mode_auth` into the bundle's
`config.yaml` alongside the existing `mode: client` injection, signed with the
server's `id_ed25519`. For a **relay**, the provisioning seam that stamps
`mode = relay` also stamps `mode_auth` (self-signed).

### Verification in `requireMode`

`requireMode` (in `internal/cli/root.go`) gains a verification step **after**
the mode-is-allowed check:

1. Canonicalize + allow-list check (unchanged, plus Part 3).
2. If `cfg.Mode` is set **and** `mode_auth` is present: rebuild the canonical
   payload from the current on-disk `mode` and this profile's identity, and
   `modeauth.Verify`. On failure return a clear error:
   `mode signature invalid — the 'mode' field was modified or the profile is
   inconsistent; re-enroll (server) / re-import (client) / re-create (relay)`.
3. If `mode_auth` is **absent** (legacy profile predating this feature): allow
   with a single `slog.Warn` ("unsigned mode; re-enroll/re-import to sign").
   This is the backward-compat path so existing servers/clients and the live
   relay are not locked out. The relay profile additionally **self-heals**:
   on load it re-signs its own `mode_auth` if missing (it holds its own key).

Note the honest limitation, documented at the `modeauth` package and at
`requireMode`: verification is local, so it is tamper-evidence, not a wall.

## Part 3 — Close CLI gating gaps (strict silos)

Every command that *acts in a role* calls `requireMode` for exactly that
role. Audit result — add the missing gates:

- `tw server join-relay` (`runServerJoin`) → `requireMode("server")`. Fresh
  installs have unset mode, which stays permissive (see below), so the first
  join still works and *sets* the mode.
- `tw client listen` (`runClientListen`) → `requireMode("client")`.
- `tw proxy set` / `tw proxy clear` / `tw proxy show` (`runProxySet` /
  `runProxyClear` / `runProxyShow`): the upstream proxy applies to both
  server and client transports → `requireMode("server", "client")`.

**Unset mode stays permissive.** An unset mode is the bootstrap state: it is
how a fresh install picks a role (`tw relay create`→relay,
`tw server join-relay`→server, importing a client bundle→client). `modeError`
already returns nil for `""`; keep it. Once mode is set, only that role's
commands pass — switching the config to another mode blocks the old role's
ops (and now fails the signature too). Unset cannot leak because nothing is
provisioned yet.

**Shared infra commands remain available in every mode** (they manage the
tool, not a role): `tw config …`, `tw service …`, `tw dashboard`,
`tw completion`, and the global `--log-level`/`--log-format` flags. These get
no `requireMode`.

## Part 4 — Fix `export-user` description

`internal/cli/export_user.go` `Short`: replace
"Issue a user as an encrypted client context bundle (server only)" with
"Issue a user as a client context bundle (server only; unprotected — send
over a trusted channel)". The bundle is AES-GCM sealed under an **empty**
passphrase (`cryptobox.Encrypt(…, "")`), i.e. openable by anyone, so calling
it "encrypted" overstates protection. The existing `Long` text already says
so; this aligns the one-line summary. (`internal/cli/create_relay.go`'s use
of "encrypted tunnel" is accurate and unchanged.)

## Testing

- Unit: `modeauth` Sign/Verify round-trip, tampered-mode fails, tampered-
  identity fails, wrong-issuer fails, empty/garbage inputs. `CanonicalMode`
  admin→relay. `modeError` unchanged behavior + new gated commands.
  A `mode: admin` config fixture loads as `relay`.
- e2e (extend existing scenarios, no new topology):
  - `RelayInstall`/`Contexts`: relay profile shows role `relay` and carries a
    valid `mode_auth` (self-signed).
  - `ServerJoin`: after `--apply`, the server config has `mode: server` with
    a `mode_auth` signed by the relay; flipping `mode` to `relay` and running
    a relay command fails with the signature error.
  - `UserLifecycle`: the imported client context has a `mode_auth` signed by
    the server; a relay/server command from the client fails the gate.
  - A gating check: `tw server join-relay` refused from a client-mode
    profile; `tw client listen` refused from a server-mode profile.
- `make e2e` green; `coverage.yaml` maps any newly-gated command paths.

## Migration & compatibility

- Existing `mode: admin` configs and `role: admin` contexts auto-migrate;
  no user action.
- Existing **unsigned** server/client profiles keep working (legacy-tolerated
  with a warning) until re-enrolled / re-issued, which signs them. The relay
  self-heals its own signature on load.
- `JoinResponse` gains additive fields; version unchanged; a new relay talking
  to an already-applied old server, or vice-versa, degrades to the
  legacy-unsigned path rather than erroring.

## Non-goals

- Remote/relay-side enforcement of mode (explicitly out — nothing mode-related
  is stored on the relay; the relay enforces via keys/certs it already holds).
- Passphrase-protected user bundles (a separate, previously-removed feature).
- Any change to the actual key/PKI trust chain — this spec adds a signature
  *over the mode field only*, layered on the unchanged keys.

## Rejected alternatives

- **Deny-everything-until-role-chosen**: adds a mandatory `tw init` step to
  every fresh install and e2e bootstrap for no real gain; unset can't leak.
- **Hard cutover on the rename**: would break the live field relay until
  hand-edited. Auto-migration is strictly better.
- **Storing mode attestations on the relay**: violates the user's constraint
  and is unnecessary — `authorized_keys` already encodes relay-vs-server.
