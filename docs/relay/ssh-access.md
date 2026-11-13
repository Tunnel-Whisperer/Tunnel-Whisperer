# Relay SSH Access

The relay is administered over SSH — but by default, that SSH is reachable
**only through the encrypted tunnel**, never from the internet. This page
explains the access model, the `--ssh-open` escape hatch, how to close it
again, and how the relay's `authorized_keys` file enforces all of it.

## The default: tunnel-only

On a freshly provisioned relay (without `--ssh-open`):

- `sshd` listens on `127.0.0.1` only (a tw drop-in at
  `/etc/ssh/sshd_config.d/99-tw-localhost.conf`),
- the firewall denies port 22 (only 80 and 443 are open),
- tw's admin key line in `authorized_keys` is pinned `from="127.0.0.1"`, so
  even if port 22 were reachable, the key would only authenticate for
  connections arriving at loopback — which in practice means connections that
  egressed from the VLESS tunnel on the relay itself,
- password authentication is off. Always, in every configuration.

Management access is therefore:

```bash
tw relay ssh
```

This opens a temporary Xray tunnel to the relay (VLESS over HTTPS, admitted by
the mTLS gate like any other connection), SSHes to the relay's loopback
through it, and gives you a full interactive shell (the tw SSH user has
passwordless sudo). The dashboard offers the same thing as a browser terminal
on the Relay page. When the shell exits, the tunnel is torn down.

The result: an attacker scanning the relay sees exactly two open ports, 80 and
443, both answering only to holders of a CA-issued client certificate. There
is no SSH surface at all.

## The escape hatch: `--ssh-open`

Provisioning with `--ssh-open` (flag or wizard prompt) trades some of that
surface for direct access:

- `sshd` listens on `0.0.0.0` and the firewall allows port 22,
- tw's admin key is written **unpinned** — no `from="127.0.0.1"` — so the tw
  key works directly over port 22 as well as through the tunnel,
- password authentication stays off, and **tenant server keys stay pinned**
  to the tunnel regardless — `--ssh-open` never widens what tenants can do.

Use it when you want a recovery path that doesn't depend on the tunnel being
healthy, or during initial bring-up. Keys outside tw's management — e.g. the
root key your cloud provider seeded — are your own responsibility either way:
the install script preserves foreign `authorized_keys` entries and never
audits them.

## Closing port 22 again

From the dashboard's Relay page, **Close Port 22** reverts the relay to
tunnel-only (it connects over direct SSH if possible, else through the
tunnel):

1. tenant accounts synced with the registry (the same sync as
   [`tw relay sync-tenants`](tenants.md#migrating-to-per-tenant-relay-accounts)),
2. `ufw deny 22/tcp`,
3. `sshd` back to `ListenAddress 127.0.0.1`,
4. tw's admin line in the admin account's `authorized_keys` rewritten with
   the admin key **re-pinned** `from="127.0.0.1"`. This restores the tunnel-only
   invariant, not just the firewall rule,
5. sshd restarted; local metadata updated so status reflects the change.

Re-running the install script without `--ssh-open` achieves the same end state
(it resets firewall rules and the sshd drop-in on every run — see the
[install-script contract](provisioning.md#the-install-script-contract)).

## Anatomy of the relay's `authorized_keys`

The admin and the tenants log in as **different** relay accounts. The tw
lines below are re-rendered from the tenant registry on every enroll,
un-enroll, close-SSH operation and `tw relay sync-tenants`. A tenant
account's `authorized_keys` is rewritten in full; in the admin account's
only tw's lines change, and other keys (e.g. your own maintenance key) are
kept.

The **admin account** (`ubuntu` by default, passwordless sudo) holds the
admin key and never a tenant key:

```text
from="127.0.0.1" ssh-ed25519 AAAA...        # pin absent on --ssh-open relays
```

The admin line has no `restrict`: the admin gets a shell, sudo, and
unrestricted forwarding (needed, among other things, to reach the relay
Xray's management API for live tenant changes). Its only constraint is the
`from="127.0.0.1"` pin — present by default, absent only on `--ssh-open`
relays, restored when SSH is closed. Relays set up by older versions also
kept every tenant's key here. The first sync removes them (see
[Tenants](tenants.md#migrating-to-per-tenant-relay-accounts)).

**Each tenant** has its own account, `twt-` followed by 24 hex characters derived from the server-id,
whose `authorized_keys` (`/var/lib/tw/tenants/<account>/.ssh/authorized_keys`,
root-owned) holds exactly one line:

```text
from="127.0.0.1",restrict,command="/usr/local/lib/tw/tw-tenant-request",port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:20000",permitlisten="127.0.0.1:40000" ssh-ed25519 AAAA...
```

The second `permitlisten` (tunnel port + 20000) is the tenant's **enroll
port** — the loopback listener their own `tw server user invite`
publishes to serve the client-enrollment PAKE exchange over. It's just
another reverse forward, confined the same way as the tunnel port: a tenant
can publish exactly these two ports and nothing else.

Tenant lines, option by option:

| Option | Effect |
| ------ | ------ |
| `from="127.0.0.1"` | Key only authenticates through the tunnel — never over public port 22, even on `--ssh-open` relays. |
| `restrict` | Disables all port forwarding, agent/X11 forwarding, PTY allocation and `~/.ssh/rc`. It does **not** block exec or shell requests on its own. |
| `command="/usr/local/lib/tw/tw-tenant-request"` | Every session runs the tenant request client instead of what the tenant asked for, so the tenant cannot run commands. |
| `port-forwarding` | Re-enables just port forwarding (the one thing a tenant needs). |
| `permitlisten="127.0.0.1:<port>"` (appears twice) | The tenant's reverse (`-R`) forwards may bind only its two allocated ports: the tunnel port and the tunnel port + 20000 (the enroll port used by `tw server user invite`). |
| `permitopen="127.0.0.1:1"` | Local (`-L`) forwarding is pinned to a dead sentinel port — effectively disabled, so tenants can't dial the relay's loopback services (e.g. the Xray management API). |

The sshd drop-in `/etc/ssh/sshd_config.d/98-tw-tenants.conf` enforces the
same on the server side for every `twt-*` account: `ForceCommand` to the
same program, `AllowTcpForwarding remote`, `PermitTTY no`, and no agent, X11,
stream-local or tunnel forwarding. The tenant model is described on
[Tenants](tenants.md#tenant-accounts-on-the-relay).

Because the files are fully re-rendered each time, hand-edits to tw-managed
lines don't survive the next enroll/un-enroll — and don't need to: the
rendered state *is* the intended state, and a corrupted file self-heals on the
next operation.

## Troubleshooting access

**`tw relay ssh` fails.** Run `tw relay test` first — it isolates the failing
layer:

- *DNS fails* — the relay domain doesn't resolve (yet). Check the A record.
- *HTTPS/Caddy fails* — Caddy is down or unreachable: VM off, ports 80/443
  closed, or ACME certificate not issued yet (first boot can take several
  minutes).
- *Xray + SSH fails* — the mTLS handshake, the VLESS layer, or SSH auth is
  broken. Verify you're on the admin machine (or one that imported the admin
  bundle): the client certificate and the admin SSH key must match what the
  relay was rendered with. On a server, this step logs in as the server's
  tenant account and sends a `list` request to the tenant handler instead of
  running a command.

**Direct `ssh <user>@relay` is refused.** Expected on a default relay: port 22
is firewalled, sshd only listens on loopback, and the tw key is pinned to the
tunnel. Direct SSH with the tw key only works on a relay provisioned
`--ssh-open`.

**Locked out completely** (tunnel broken *and* no direct SSH): use your cloud
provider's serial/web console or recovery mode, or any non-tw key you kept in
`authorized_keys`. As a last resort, re-run the install script
(`tw-install-<domain>.sh`) as root on the VM — it rebuilds the relay's entire
tw state idempotently. If you re-run it, remember it resets the tenant set to
just the admin slot; re-enroll servers afterwards (see
[Tenants](tenants.md)).

**A tenant reports its tunnel won't come up** after relay maintenance. Check
`tw relay get-servers` — if the tenant is missing or `down`, the relay config
may have been rebuilt (install-script re-run). Re-enroll the tenant; the
enroll operation rewrites all relay-side state for every registered server.
If the tunnel went down right after a tenant sync, the server probably runs
an older `tw` that still logs in as the admin account. Upgrade it (see
[Tenants](tenants.md#troubleshooting-tenant-accounts)).

**The dashboard's relay terminal gives no shell on a server profile.**
Expected: a server's relay account has no shell, and every session runs the
tenant request program. Only the relay admin context (`tw relay ssh`, or the
terminal in the admin's dashboard) opens a relay shell.
