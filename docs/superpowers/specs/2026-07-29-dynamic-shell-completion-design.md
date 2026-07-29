# Dynamic shell completion for object-naming arguments

**Date:** 2026-07-29
**Status:** Approved

## Goal

`tw config use-context <TAB>` (and every other command whose positional
argument names a stored object) offers the actual stored objects as
completion candidates, with rich zsh descriptions.

## Scope — commands and their candidates

| Command | Arg | Candidates | Description column |
|---|---|---|---|
| `config use-context <name\|id>` | 1 | context names **and** short IDs | `role user@relay` (ID entries: `id of <name>`) |
| `config delete-context <name\|id>` | 1 | same | same |
| `config export [name\|id]` | 1 | same | same |
| `config rename-context <old> <new>` | 1 only | same | same; 2nd arg (new name) gets no completion |
| `user delete <name>` | 1 | usernames | tunnel count + active/applied state |
| `user edit <name>` | 1 | usernames | same |
| `export-user <name>` | 1 | usernames | same |
| `unregister <name>` | 1 | usernames | same |
| `apply [name...]` | all | usernames **minus names already on the line** | same |
| `relay un-enroll-server <server-id>` | 1 | server-ids from the local registry | `port <n>, enrolled <date>` |
| `server app edit <name>` | 1 | application template names | mapping count |
| `server app delete <name>` | 1 | same | same |

`config import <bundle.zip>` and `relay enroll-server <join-request.json>`
keep cobra's default file completion — they take files.

## Mechanism

- New `internal/cli/completions.go`: one `ValidArgsFunction` helper per
  object kind — `completeContexts`, `completeUsers` (exclusion-aware for
  `apply`), `completeServerIDs`, `completeApps` — attached to the commands
  above in their `init()`s via `cmd.ValidArgsFunction = ...`.
- Each helper: `ops.New()` + the existing list call (`ListContexts`,
  `ListUsers`, `ListServers`, `ListApplications`). All are fast local
  filesystem reads — completion never dials the relay or the daemon.
- Candidates are `"value\tdescription"` strings (cobra's format; zsh shows
  the description column).
- Every helper returns `cobra.ShellCompDirectiveNoFileComp`, and on ANY
  error returns `(nil, NoFileComp)` — tab completion must never print an
  error or fall back to filenames.
- Pure formatting/filter logic lives in standalone funcs taking the info
  slices (`contextCandidates([]ops.ContextInfo)`, etc.) so it unit-tests
  without a config dir.
- No mode gating inside completers (`requireMode` stays on the commands):
  a profile without the data yields an empty list naturally.
- Known acceptable side effect: `ListContexts` may lazily backfill old
  index entries (one local write, pre-existing behavior).

## Testing

- Unit: candidate-building funcs — names+IDs both emitted for contexts,
  empty-ID contexts emit no ID entry, `apply` exclusion filter, tab-free
  values.
- e2e (real zsh completion entry point is cobra's hidden `__complete`):
  - Contexts scenario: `tw __complete config use-context ""` lists the
    imported context name AND its short ID.
  - UserLifecycle: `tw __complete user delete ""` lists the created user.
  - SecondTenant: `tw __complete relay un-enroll-server ""` lists the
    enrolled server-id.
- coverage.yaml unchanged: no new user-facing commands (`__complete` is
  cobra-internal plumbing behind the existing `completion` command).

## Out of scope

- bash/powershell completion scripts (the `completion` command stays
  zsh-only).
- Flag-value completion.
- Completing relay-remote data (nothing completes over the network).
