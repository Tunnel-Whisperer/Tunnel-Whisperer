# Flag-based manual relay install (`tw admin create`)

Date: 2026-07-21
Status: approved

## Problem

`tw admin create` is an interactive wizard. The only scripted form is piping
positional answers on stdin (`printf 'domain\n4\nip\ny\n' | tw admin create`),
which is fragile (silently breaks if prompt order changes) and invisible in
`--help`. There is no first-class non-interactive way to register a manual
(bring-your-own-VM) relay.

## Design

### Flag surface

Added to `tw admin create`, alongside the existing `--ssh-open`:

- `--provider manual` — provider selection by name. Only `manual` is accepted
  for now; any other value errors with a message naming the supported value.
  A string flag (not a `--manual` bool) so cloud providers can be added later
  without a breaking change.
- `--domain <relay-domain>` — relay domain. Also overrides any existing
  `Xray.RelayHost` without the "keep current?" prompt.
- `--ip <public-ip>` — the VM's public IP. Manual path only: giving `--ip`
  without `--provider manual` is an error.

### Mode rule

If `--provider`, `--domain`, and `--ip` are all set, the run is **fully
non-interactive** — no prompts at all. Otherwise the wizard runs exactly as
today, with provided flags pre-answering their prompts (the "have you run the
script?" confirmation stays in wizard mode).

### Non-interactive behavior

1. If a relay is already provisioned: error
   `relay already provisioned (provider: X) — run 'tw admin destroy' first`.
   No implicit destroy in scripted runs.
2. Otherwise: generate the install script, write it to
   `tw-install-<domain>.sh` (as today), record the relay via
   `SaveManualRelay`, write the profile bundle, and print next steps
   (DNS A record → open 80/443 → run the script as root on the VM).
   The relay is recorded immediately; running the script happens after.

Canonical one-liner:

```bash
tw admin create --provider manual --domain relay.example.com --ip 203.0.113.5 --ssh-open=false
```

## Testing

- e2e: the `RelayInstall` scenario switches from the stdin pipe to the
  flag-based one-liner, proving the new path over the real topology.
  `e2e/coverage.yaml` already maps `admin create` to that scenario.
- Unit: flag-validation edge cases (unknown provider, `--ip` without
  `--provider manual`) in `internal/cli`, following the `root_test.go` pattern.

## Out of scope

- Non-interactive cloud-provider create (flags for tokens/credentials).
- `--recreate` / implicit destroy.
