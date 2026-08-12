# Zero-file enrollment: invite codes + PAKE — design

2026-08-12. Status: draft for user review. Consolidates the 2026-07-31 design
session (Approach 1 locked, then paused) plus the local/remote split pinned by
the shipped `tw relay add-server` (2026-08-04 spec).

## Problem

Every enrollment today moves a secret-bearing file over whatever channel the
humans have (email, chat, USB):

1. **Join request** (server → relay admin): leaks the server's VLESS UUID —
   enough to attempt tunnel access at the relay path.
2. **Join response** (relay admin → server): relay coordinates + mode
   signature.
3. **User bundle** (server → client, `tw config export-user`): the worst one —
   it carries the client's SSH **private key** and client-cert **private key**.
   Whoever copies the file *is* the client.

Admins demonstrably move these over insecure channels; the files are also
fiddly (right file to the right person, `--apply` the response, stale files
lying in CWD). The single-operator case is already solved without files
(`tw relay add-server`, in-process); this spec removes the files for the
**remote** case.

## Decisions (user-locked 2026-07-31)

- **Zero files, no file fallback.** Short one-time invite codes spoken/typed
  over any human channel replace all three transfers. The file-based
  join/enroll artifacts (`tw_join_*.json`, `tw_join_response_*.json`) and the
  user-bundle export path are deleted, not deprecated.
- **Approach 1: the code is a SPAKE2 (PAKE) password.** Both sides derive a
  mutually-authenticated E2E-encrypted channel from it; the relay forwards
  only ciphertext, preserving the "relay never sees plaintext" stance.
  Rejected: TLS bearer code (relay compromise reads the exchange),
  relay-hosted broker (relay must stay dumb).
- **Blocking, interactive handshake with mandatory SAS read-back.** The
  issuer's terminal waits; both ends display a short authentication string
  derived from the PAKE transcript + the enrollee's public keys. The human at
  the enrollee end reads theirs aloud; the issuer approves only on exact
  match. A code thief who redeems first burns the invite, so the legit
  human's join fails "already used" and the theft is detected; a network MITM
  is dead by PAKE. Neither the code nor the SAS needs a secure channel.
  Rejected: async pending-approval queue.
- **Invites are single-use, short-TTL (~15 min), burn on ANY redemption
  attempt** including a wrong-code guess against them. An invite encodes
  issuer-id + the PAKE secret.
- **Private keys never transit** — in both flows the enrollee generates its
  full key material locally and sends only public material up the channel.
- **Local branch is separate and already shipped**: when issuer and enrollee
  share the tw install there is no channel to authenticate — direct
  in-process enrollment (`tw relay add-server` today; client counterpart
  `tw server user create <name> --local-context` as follow-up). Codes/PAKE
  are for remote enrollees only.

## Transport: an `/enroll` route on the relay

The enrollee has no client cert yet, so it cannot pass the mTLS gate. Caddy
admission changes from `client_auth require_and_verify` to **`verify_if_given`**
(verified against current Caddy docs: a no-cert handshake completes; any
presented cert is still verified against the `trust_pool`). Tunnel paths stay
locked by the existing per-tenant CN expression matchers — an empty client
subject matches no tenant block and falls through to 404, fail-closed.

New route: `/enroll/<issuer-id>` → `reverse_proxy` to the issuer's daemon
through the issuer's **existing reverse SSH tunnel** (the same port the
tenant already holds on the relay; the relay admin's own slot for
server-enrollment invites). The forward is open only while an invite is live;
`handle_errors` maps upstream-refused to 404 so an idle relay looks identical
to a probe. Rejected alternatives: second SNI (DNS + cert friction), extra
port (breaks the 80/443-only posture). Accepted trade-off: probes can now
complete a TLS handshake and collect 404s — arguably *less* fingerprintable
than today's loud mTLS rejection.

## Flows

Issuer side mints a code and blocks waiting; enrollee side is a **single
command for both roles** — the issuer tells the enrollee its role inside the
PAKE channel, the enrollee never chooses:

```
tw join <relay-host> <code>
```

`tw join` is deliberately top-level and mode-less: the enrollee has no mode
yet, which is exactly why it must not be `requireMode`-gated (the lesson from
`--new-context` being unusable behind the join gate).

### Server enrollment (issuer = relay admin)

1. Admin: `tw relay invite` → prints code + waits. Opens the `/enroll` window.
2. Enrollee: `tw join <relay-host> <code>` → PAKE handshake → generates full
   identity locally (SSH keypair, CA, client cert, UUID) — today's
   `GenerateJoinRequest` material — and sends the **public** half + UUID up.
3. Both terminals show the SAS; admin approves on read-back match.
4. Admin side runs the real `EnrollServer` (registry + port allocation +
   relay rewrite + live tenant add + `signServerMode`) and sends
   path/remote_port/ssh_user/mode_sig down.
5. Enrollee applies via the existing `ApplyJoinResponse` logic
   (verify-before-persist) into a fresh context. Born signed.

### Client enrollment (issuer = server operator)

1. Server: `tw server user create <name> --invite` → creates the user, prints
   code + waits.
2. Enrollee: `tw join <relay-host> <code>` → PAKE → generates SSH keypair +
   cert **CSR** + UUID locally, sends pubkey/CSR/UUID up.
3. SAS read-back, server operator approves.
4. Server signs the CSR, appends the pubkey to `authorized_keys`
   (existing `appendAuthorizedKey`), pushes the UUID to the relay's Xray via
   its gRPC API, and sends signed cert + CA cert + client config down.
5. Enrollee writes a locally-born client context. No private key ever left
   the client machine — a strict improvement over today's bundle.

### Port handling (replaces port_overrides)

`port_overrides` existed only because config.yaml was authored server-side
and re-imports clobbered local edits. With locally-born configs it is
**deleted**. The server sends down only *server-side* ports plus optional
suggested local ports; `tw join` preflight-binds each local port on the
client during enrollment, prompts the human on conflict, and writes the
final mapping into the local config. LocalPort becomes client-owned — a plain
config edit later, nothing to survive a re-import. (Post-enrollment mapping
updates when the admin adds services: follow-up feature — discover permitted
ports over the live tunnel; not in this spec.)

## Deletions

- `tw server join-relay` file flow (request/response JSON, `--apply`,
  `--new-context`) — superseded by `tw join`.
- `tw relay enroll-server <file>` — superseded by `tw relay invite`;
  `EnrollServer(req)` core stays, only the file front-end goes.
- `tw config export-user` and the user-bundle flow (private-key transfer).
- `port_overrides` mechanism end to end.
- Dashboard "Enroll a Server" file upload/download — replaced by the invite
  flow's CLI; dashboard mirror is follow-up (CLI-first rule).

Kept: `tw config export` / `import` of one's **own** contexts (backup and
machine migration — the relay bundle is the relay's only backup). Open
question below confirms this scoping, since the 2026-07-31 wording ("bundle
export deleted") was broader.

## Open questions (settle at user review, before writing-plans)

1. **Code format/entropy** — proposal: `NN-word-word` wormhole-style
   (channel-id + 2 diceware words); low entropy is safe *because* PAKE +
   burn-on-attempt, but pick and pin.
2. **SPAKE2 Go dependency** — candidate: the spake2 package used by
   wormhole-william; vet maintenance, or vendor a minimal implementation.
3. **Protocol framing** over the `/enroll` HTTP channel (single POST +
   long-poll? WebSocket? chunked bidi?) — pick what Caddy `reverse_proxy`
   over the SSH-forwarded port handles most simply.
4. **TTL value** and whether the issuer command has a `--ttl` flag.
5. **Command names** — brainstorm said `tw enroll new`; this spec adapts to
   the post-rename grouped CLI as `tw relay invite` / existing
   `tw server user create --invite` / top-level `tw join`. Confirm.
6. **Export scoping** — confirm own-context export/import survives (backup),
   only *issuing-others'-identities* files die.
7. **Enroll-window port** allocation/registration in the registry for the
   relay admin's own slot.

## Testing

- Unit: invite mint/burn/TTL state machine; SAS derivation is deterministic
  from transcript+pubkeys and differs on any pubkey substitution; CSR sign
  path; preflight-bind conflict prompt logic.
- e2e (extends the compose suite):
  - `CertlessProbe`: TLS handshake with no client cert reaches 404 on tunnel
    paths and on `/enroll/*` when no invite is live (verify_if_given did not
    open anything).
  - `InviteServer`: mint on admin, `tw join` on a fresh container, scripted
    SAS confirm, then `tw server start` + `tw server test` green over the
    real tunnel; no "mode is unsigned" anywhere.
  - `InviteClient`: `user create --invite` on server, `tw join` on client
    container, `tw client connect` + data through the forwarded port.
  - `InviteBurn`: second redemption of a used code fails; wrong code against
    a live invite burns it; expired invite refused.
  - coverage.yaml rows for every new/changed command; file-flow scenarios
    (`ServerJoin` et al.) rewritten to the invite flow, not exempted.
- Docs: enrollment pages rewritten around invites; tenants.md handshake
  diagram replaced; migration note for existing file-flow users.

## Out of scope

- Dashboard mirror of invite issuance/approval (CLI-first; follow-up).
- `tw server user create --local-context` (local client counterpart —
  separate small spec, same in-process pattern as add-server).
- Post-enrollment port-mapping discovery over the live tunnel.
- Client-side RemotePort elimination via per-tenant freedom `redirect`
  outbound on the relay (related 2026-07-31 finding; fold in only if the
  user says so — otherwise separate).
