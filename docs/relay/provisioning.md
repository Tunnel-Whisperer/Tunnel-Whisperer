# Provisioning the Relay

The relay is a lightweight VM (Ubuntu 24.04) that serves as the rendezvous
point between servers and clients. Both sides connect outbound to it over
HTTPS — no inbound ports needed on either side. There are two ways to get one:

- **Cloud** — `tw` provisions a VM for you via Terraform on Hetzner,
  DigitalOcean, or AWS.
- **Manual** — you bring your own VM and run a generated install script on it
  as root.

Both paths render the exact same relay configuration (Caddyfile with the mTLS
gate, Xray `config.json`, the tw-managed SSH setup) from the same code, so
they cannot drift.

```bash
tw relay create
```

Creating a relay **stamps the active profile's mode to `relay`** (only if the
mode was unset — server and client profiles are blocked from the command) and,
on success, emits the **admin bundle** in the current directory (domain
sanitized into the name: `tw_relay-example-com.twctx` for
`relay.example.com`).

!!! danger "Keep the admin bundle safe"
    The `.twctx` bundle is the portable relay identity: admin SSH key, CA,
    certificates, and configuration. Whoever holds it administers the relay —
    and there is **no recovery** if it is lost. Store it like a private key.
    (You can re-create it later from the admin machine with `tw config export`.)

## Public SSH: the `--ssh-open` question

Before anything else, the wizard asks one security question: **should the
relay's SSH port 22 be open to the internet?** The default is **no**.

- **Default (closed)** — `sshd` listens on `127.0.0.1` only, the firewall
  blocks port 22, and tw's admin key is pinned `from="127.0.0.1"` so it only
  authenticates for connections arriving through the encrypted tunnel.
  Management access is `tw relay ssh` (through the tunnel) — nothing else.
- **`--ssh-open`** — `sshd` listens on `0.0.0.0`, the firewall allows port 22,
  **and** the tw admin key is written *unpinned*, so `tw`'s own key also works
  directly over port 22. Password authentication is off in both cases; tenant
  server keys stay pinned to the tunnel regardless.

You can close port 22 later from the dashboard, which also restores the
loopback pin on the admin key. The full model — including exactly which
`authorized_keys` lines change — is on [SSH access](ssh-access.md).

## Cloud provisioning (Terraform)

Requires `terraform` in `PATH`
([install](https://developer.hashicorp.com/terraform/install)). The wizard
walks through:

1. **Relay domain** — e.g. `relay.example.com` (you need control of its DNS).
2. **Cloud provider** — Hetzner, DigitalOcean, or AWS.
3. **Credentials** — API token (Hetzner/DO) or Access Key + Secret (AWS). The
   wizard prints the exact console page where to generate them, then validates
   them against the provider API before touching anything.
4. **Confirm and provision** — `tw` generates cloud-init + Terraform config
   into `<config-dir>/relay/` and runs `terraform init` and
   `terraform apply`. SSH keys, the Xray UUID, and the CA/client certificates
   are generated first if missing.
5. **DNS + readiness** — the wizard prints the relay IP and the A record to
   create, then polls until the domain resolves and Caddy obtains a TLS
   certificate (up to 15 minutes for cloud-init + ACME). Finally it streams the
   relay's cloud-init log as a sanity check.

| Provider | Instance | Default region | Credential |
| -------- | -------- | -------------- | ---------- |
| Hetzner | cx22 | nbg1 (Nuremberg) | API Token |
| DigitalOcean | s-1vcpu-1gb | fra1 (Frankfurt) | API Token |
| AWS | t3.micro | us-east-1 | Access Key + Secret Key |

All providers use an Ubuntu 24.04 image on the smallest sensible tier — the
relay only shuffles encrypted bytes and needs almost no resources.

### What gets installed

The VM is configured entirely by cloud-init (rendered at provision time on the
admin machine — the relay never generates anything itself):

- An SSH user (`ubuntu` by default) holding the tw-managed `authorized_keys`
- **Caddy** from the official apt repository — TLS/ACME termination with
  `client_auth require_and_verify` against the per-tenant CA trust pool
- **Xray** at pinned version `v26.6.27` — VLESS inbound on localhost with
  XHTTP transport
- The rendered Caddyfile and Xray `config.json`, plus the admin's CA
  certificate at `/etc/caddy/ca/<server-id>.crt`
- SSH locked to localhost (or opened, with `--ssh-open`), password auth off
- Firewall: deny all incoming, allow 80/tcp + 443/tcp (+ 22/tcp with
  `--ssh-open`)

!!! info "Version pinning"
    The relay's Xray version is pinned to stay wire-compatible with the
    `xray-core` embedded in the `tw` binary (which is itself pinned in
    `go.mod`). Don't upgrade it by hand on the VM — re-render instead.

### Re-provisioning

If a relay already exists (Terraform state or a manual marker present), the
wizard offers to destroy and recreate it. TLS certificates are saved from the
old relay before destruction and restored on the new one, so you don't burn
Let's Encrypt rate limits on repeated rebuilds.

## Manual provisioning (bring your own VM)

For an existing VPS or an unsupported provider, choose **Manual** in the
wizard — or skip the prompts entirely:

```bash
tw relay create --provider manual --domain relay.example.com --ip 203.0.113.7 [--ssh-open]
```

The non-interactive form requires `--provider manual`, `--domain`, and `--ip`
together (`--ip` is only valid with `--provider manual`; no other `--provider`
value is accepted). `--ssh-open` defaults to off. If a relay is already
provisioned, the non-interactive run **fails** instead of prompting — run
`tw relay destroy` first; scripted runs never destroy infrastructure
implicitly.

Both forms:

1. Generate the install script and write it to **`tw-install-<domain>.sh`** in
   the current directory (mode `0700` — it carries the tunnel UUID).
2. Record the relay locally (domain, IP, `ssh_open`) and emit the admin
   bundle.

You then finish on the VM side:

1. Create a VM (Ubuntu/Debian) with a public IP and point the domain at it
   (DNS A record).
2. Open ports 80 and 443 in its firewall / provider security group.
3. Copy `tw-install-<domain>.sh` to the VM and run it **as root**.

The interactive wizard asks for the VM's public IP and whether you've run the
script; the non-interactive form records the relay immediately and prints the
same checklist — running the script afterwards is fine.

### The install-script contract

The script is **idempotent by design: it cleans, then reinstalls**. Re-running
it on the same VM is the supported way to repair a relay or to change the
`--ssh-open` setting. On every run it first removes all previous tw state —
per-tenant CA certs in `/etc/caddy/ca/`, the Caddyfile, the Xray install and
config, the tw sshd drop-in, and all firewall rules (including a stale
`allow 22` from an earlier `--ssh-open` run) — then installs everything fresh.

What it deliberately does **not** touch:

- the OS and the host's SSH host keys (no host-key warnings after a re-run),
- `authorized_keys` entries it doesn't manage — your own maintenance keys
  survive; only a stale copy of tw's key line is stripped before the current
  one is written.

!!! warning "Re-running resets the tenant set"
    Because the script re-renders the relay from the *admin's own* slot only,
    a re-run removes enrolled server tenants from the relay's config. Re-add
    them afterwards by re-running the enrollment (see
    [Tenants](tenants.md)) — their registry entries on the admin machine are
    untouched.

## Testing the relay

```bash
tw relay test
```

A 3-step diagnostic (also available as `tw server test` / `tw client test` on
the other roles):

1. **DNS** — the relay domain resolves.
2. **HTTPS (Caddy)** — a TLS connection presenting this machine's client
   certificate succeeds; without a cert to present, a "certificate required"
   rejection still proves Caddy is up and the mTLS gate is active.
3. **Xray + SSH** — a full tunnel is established and a real SSH command is
   executed on the relay.

## Destroying the relay

```bash
tw relay destroy
```

- **Cloud relays** — saves the Caddy TLS certificates for reuse (best-effort,
  30-second timeout if the relay is unreachable), runs
  `terraform destroy`, then removes the local relay state. AWS asks for
  credentials again; Hetzner/DO reuse the stored token.
- **Manual relays** — nothing is executed on the VM; `tw` forgets the relay
  marker and local state. Decommission the VM yourself.

In both cases users are marked inactive (their relay coordinates are gone) and
the relay host is cleared from config, so `tw relay status` reflects the
destroy immediately.
