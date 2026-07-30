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

1. `ufw deny 22/tcp`,
2. `sshd` back to `ListenAddress 127.0.0.1`,
3. `authorized_keys` rewritten with the admin key **re-pinned**
   `from="127.0.0.1"` — restoring the tunnel-only invariant, not just the
   firewall rule,
4. sshd restarted; local metadata updated so status reflects the change.

Re-running the install script without `--ssh-open` achieves the same end state
(it resets firewall rules and the sshd drop-in on every run — see the
[install-script contract](provisioning.md#the-install-script-contract)).

## Anatomy of the relay's `authorized_keys`

All access — admin and tenants — goes through **one** SSH user on the relay
(`ubuntu` by default). Authorization is entirely per-key, in a single
tw-managed `authorized_keys` file that is **rewritten in full** from the
tenant registry on every enroll, un-enroll, and close-SSH operation. Its
shape:

```text
from="127.0.0.1" ssh-ed25519 AAAA... admin-key        # pin absent on --ssh-open relays
from="127.0.0.1",restrict,port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:20000" ssh-ed25519 AAAA... tenant-1
from="127.0.0.1",restrict,port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:20001" ssh-ed25519 AAAA... tenant-2
```

**The admin line** (always first) has no `restrict`: the admin gets a shell,
sudo, and unrestricted forwarding (needed, among other things, to reach the
relay Xray's management API for live tenant changes). Its only constraint is
the `from="127.0.0.1"` pin — present by default, absent only on `--ssh-open`
relays, restored when SSH is closed.

**Tenant lines** are forwarding-only, option by option:

| Option | Effect |
| ------ | ------ |
| `from="127.0.0.1"` | Key only authenticates through the tunnel — never over public port 22, even on `--ssh-open` relays. |
| `restrict` | Denies everything: shell, exec, agent/X11 forwarding, *and* all port forwarding. |
| `port-forwarding` | Re-enables just port forwarding (the one thing a tenant needs). |
| `permitlisten="127.0.0.1:<port>"` | The tenant's reverse (`-R`) forward may bind only its own allocated port. |
| `permitopen="127.0.0.1:1"` | Local (`-L`) forwarding is pinned to a dead sentinel port — effectively disabled, so tenants can't dial the relay's loopback services (e.g. the Xray management API). |

Because the file is fully re-rendered each time, hand-edits to tw-managed
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
  relay was rendered with.

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
