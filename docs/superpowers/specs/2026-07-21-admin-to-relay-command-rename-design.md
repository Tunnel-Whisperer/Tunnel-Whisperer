# `tw admin` → `tw relay` command rename — design

Date: 2026-07-21
Status: approved

## Problem

Every `tw admin` command operates on the relay, but none of them say so:
`tw admin create` (create what?), `tw admin ssh` (ssh where?), `tw admin
enroll` (enroll what?). A `tw admin relay …` subgroup was considered and
rejected (redundant nesting — admin ≡ relay operator).

## Decision (user-confirmed)

Rename the command group to name the object being managed, matching the
architecture's three components (`tw relay` / `tw server` / `tw client`):

```
tw relay
  create            provision the relay (wizard or --provider manual …)
  destroy           tear it down
  ssh               shell on the relay
  test              connectivity test
  status            relay status
  enroll-server     <join-request.json>  admit a server (was: admin enroll)
  get-servers       list registered servers (was: admin servers;
                    plural to match `tw config get-contexts`)
```

- **Hard rename** — `tw admin` disappears; no hidden aliases (pre-1.0,
  self-distributed).
- **Internal role string stays `admin`** — config `mode: admin`,
  `requireMode("admin")`, the contexts ROLE column, and derived names like
  `admin-hds-t2` are unchanged. "admin" is the profile; `tw relay …` names
  the object. Existing installs keep working.
- Join flow reads: `tw server join <domain>` → `tw relay enroll-server
  tw_join_….json` → `tw server join --apply …`.

## Scope

- `internal/cli`: `adminCmd` becomes `relayCmd` (`Use: "relay"`); files
  `admin.go` → `relay.go`, `admin_enroll.go` → `relay_enroll.go`; command
  `Use`/help strings and any user-facing text that says `tw admin …`.
- e2e: every `tw admin …` invocation, scenario descriptions, and
  `coverage.yaml` keys move to the new names.
- Docs (`docs/`): all `tw admin` references.

## Testing

Existing coverage carries over: RelayInstall (create/test/status),
ServerJoin + SecondTenant (enroll-server, get-servers), coverage_test
enforces the renamed keys. Full `make e2e` must pass; `tw admin` must no
longer parse.

## Out of scope

- Renaming the internal mode/role string.
- Restructuring `tw server` / `tw client` / `tw config` groups.
