# Mode Integrity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the `admin` mode/role to `relay` (with auto-migration), add a tamper-evident signature over each profile's `mode`, close CLI role-gating gaps, and fix the `export-user` description.

**Architecture:** `relay` becomes the canonical mode string; `config.Load()` migrates `admin`→`relay` in place. A new pure `internal/ops/modeauth` package signs `(mode, identity)` with ed25519; the relay self-signs, signs servers in the join response, and servers sign clients in the user bundle. `requireMode` verifies the signature (legacy-unsigned tolerated with a warning) after the existing allow-list check. The real security boundary is unchanged (relay `authorized_keys` `restrict` + mTLS/PKI); the signature is local tamper-evidence only.

**Tech Stack:** Go 1.26, `golang.org/x/crypto/ssh` (ed25519 signers), Cobra, YAML config, Docker-Compose e2e.

**Spec:** `docs/superpowers/specs/2026-07-22-mode-integrity-design.md` — read it first, especially the "Security model" section.

## Global Constraints

- **NO git commits in this plan** — the user drives git. Skip every "commit" convention; leave the tree dirty for the controller to handle.
- Verify = `go build ./...` + `go vet ./...` pass; touched-package unit tests pass; full `make e2e` passes at the end (CLAUDE.md done-criteria).
- Canonical mode strings are exactly `relay` | `server` | `client`; `admin` is a **read-time alias** migrated to `relay`, never re-emitted.
- The mode signature is **tamper-evidence, not a security wall** — say so in the `modeauth` package doc and at `requireMode`. Never store anything mode-related on the relay.
- Signature payload is canonical and versioned: `tw-mode-v1\n<mode>\n<identity>`, where `<identity>` is the profile's own `id_ed25519.pub` contents trimmed. Always canonicalize `mode` (admin→relay) BEFORE signing or verifying.
- `mode_auth` YAML block shape: `mode_auth:\n  sig: <b64>\n  issuer: <b64>` (base64 std encoding; `issuer` is the signer's ed25519 public key raw bytes, `sig` the raw ed25519 signature).
- Errors wrapped `fmt.Errorf("...: %w", err)`; structured logging via `slog`.
- Module path `github.com/tunnelwhisperer/tw`; own ssh/xray packages import as `twssh`/`twxray`.
- After the final task, build BOTH bins (`make build`, `make build-windows`) and stage `bin/tw.exe` to `/mnt/c/Users/alial/Downloads/tw.exe`.

---

### Task 1: Canonical mode + config migration

**Files:**
- Modify: `internal/config/config.go` (ValidMode ~28, Load ~195)
- Modify: `internal/ops/ops.go` (SetMode ~120, Mode doc ~112)
- Test: `internal/config/config_test.go` (create if absent)

**Interfaces:**
- Produces: `func CanonicalMode(m string) string` (admin→relay, else unchanged); `ValidMode` now accepts `relay`; `Load()` canonicalizes `cfg.Mode` and rewrites once when it changed.

- [ ] **Step 1: Write the failing test**

Create/append `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalMode(t *testing.T) {
	cases := map[string]string{"admin": "relay", "relay": "relay", "server": "server", "client": "client", "": ""}
	for in, want := range cases {
		if got := CanonicalMode(in); got != want {
			t.Errorf("CanonicalMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadMigratesAdminToRelay(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("mode: admin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "relay" {
		t.Fatalf("loaded mode = %q, want relay", cfg.Mode)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if string(data) == "mode: admin\n" {
		t.Error("config.yaml still says mode: admin — migration was not persisted")
	}
}

func TestValidModeAcceptsRelayNotAdmin(t *testing.T) {
	if !ValidMode("relay") {
		t.Error("relay should be valid")
	}
	if ValidMode("admin") {
		t.Error("admin should no longer be a valid canonical mode")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestCanonicalMode|TestLoadMigrates|TestValidMode'`
Expected: FAIL — `undefined: CanonicalMode` and ValidMode still accepts admin.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, replace `ValidMode` and add `CanonicalMode`:

```go
// ValidMode reports whether m is a recognized canonical operating mode.
func ValidMode(m string) bool {
	return m == "server" || m == "client" || m == "relay"
}

// CanonicalMode maps legacy mode names to their canonical form. The relay
// role was historically called "admin"; it is accepted on read and rewritten
// to "relay". Any other value is returned unchanged.
func CanonicalMode(m string) string {
	if m == "admin" {
		return "relay"
	}
	return m
}
```

Update the `Mode` field comment (line ~16) to `// "server", "client", or "relay"`.

In `Load()`, after the `yaml.Unmarshal` success and before `return cfg, nil`:

```go
	if canon := CanonicalMode(cfg.Mode); canon != cfg.Mode {
		cfg.Mode = canon
		_ = Save(cfg) // best-effort in-place migration; read-only dir is not fatal
	}
```

In `internal/ops/ops.go` `SetMode`, update the error text and the `Mode` doc:

```go
// Mode returns the current operating mode ("server", "client", "relay", or "").
```
```go
	if !config.ValidMode(mode) {
		return fmt.Errorf("invalid mode: %q (must be \"server\", \"client\", or \"relay\")", mode)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ && go build ./... && go vet ./internal/config/ ./internal/ops/`
Expected: PASS; build/vet clean. (Other packages still pass `"admin"` literals — they compile fine; Task 2 renames them.)

---

### Task 2: Mechanical rename of internal `admin` literals → `relay`

**Files:**
- Modify: `internal/cli/status.go:19`, `relay_ssh.go:24`, `test_relay.go:19`, `relay_unenroll.go:29`, `create_relay.go:69`, `destroy_relay.go:27`, `relay_enroll.go:32,64` (`requireMode("admin")`→`requireMode("relay")`)
- Modify: `internal/ops/relay.go` (`cfg.Mode == "admin"` ~154, `role = "admin"` ~155, two `cfg.Mode = "admin"` ~221/~419 and their comments)
- Modify: `internal/ops/enroll.go:58` (`Role: "admin"` → `Role: "relay"`)
- Modify: `internal/config/context.go:61,65` (`role == "admin"` branch and its `"admin"` default-name prefix → `"relay"`)
- Modify: `internal/relay/caddy/config.go:18,24` (comments `"admin"` → `"relay"`)

**Interfaces:**
- Consumes: `requireMode` (unchanged signature), `config.CanonicalMode` (Task 1).
- Produces: no new symbols; the internal role/mode literal is now `relay` everywhere except the dashboard (Task 3) and the migration alias (Task 1).

- [ ] **Step 1: Apply the requireMode + ops rename**

Run:
```bash
cd "$(git rev-parse --show-toplevel)"
sed -i 's/requireMode("admin")/requireMode("relay")/g' internal/cli/*.go
```

- [ ] **Step 2: Edit the remaining literals by hand**

In `internal/ops/relay.go`: change `if cfg.Mode == "admin"` → `if cfg.Mode == "relay"`; `role = "admin"` → `role = "relay"`; both `cfg.Mode = "admin"` → `cfg.Mode = "relay"`; and reword the two nearby comments that say `requireMode("admin")` / mode "admin" to "relay".

In `internal/ops/enroll.go:58`: `Role: "admin",` → `Role: "relay",`.

In `internal/config/context.go`: the `case role == "admin":` becomes `case role == "relay":`, and the returned default-name prefix `"admin"` / `"admin-"` becomes `"relay"` / `"relay-"`. Also make the context migration/backfill canonicalize: wherever a stored `Role` is read for comparison or naming, wrap with `CanonicalMode` so an existing `role: admin` context is treated as `relay`. If `ListContexts` backfills/rewrites meta, have it write `CanonicalMode(meta.Role)`.

In `internal/relay/caddy/config.go`: update the two comments referencing `"admin"` to `"relay"`.

- [ ] **Step 3: Confirm no stray canonical-admin literals remain**

Run: `rg -n '"admin"' internal/ --type go | grep -v _test | grep -v dashboard`
Expected: no matches (dashboard is Task 3; tests are updated where they assert behavior). If any remain, they are either a human-administrator word (leave) or a miss (fix).

- [ ] **Step 4: Verify**

Run: `go build ./... && go vet ./... && go test ./internal/ops/ ./internal/config/ ./internal/cli/ ./internal/relay/caddy/`
Expected: build/vet clean. Some tests may reference old names — update any that assert `"admin"` role/mode to `"relay"` (legitimate behavior change), then re-run to green.

---

### Task 3: Dashboard rename (`admin` page → `relay`)

**Files:**
- Modify: `internal/dashboard/handlers_pages.go:59,61` (`mode == "admin"`, `renderPage(w, "admin", …)`)
- Rename: `internal/dashboard/templates/pages/admin.html` → `pages/relay.html`
- Modify: `internal/dashboard/templates/partials/nav.html`, `pages/index.html`, `pages/setup.html` (references to the admin page/role)

**Interfaces:**
- Consumes: canonical `relay` mode from `config.Load()` (Task 1). Note `handlers_pages.go` reads mode via `config.Load()`/ops, which now returns `relay` even for a legacy `admin` config, so the comparison must be against `"relay"`.

- [ ] **Step 1: Rename the template and its references**

Run:
```bash
cd "$(git rev-parse --show-toplevel)/internal/dashboard/templates"
git mv pages/admin.html pages/relay.html
```
Then grep the templates for `admin` and update role/page references (nav link labels/targets, index branch, setup copy) from the admin page/role to relay. Leave any human-administrator wording.

- [ ] **Step 2: Update the handler**

In `internal/dashboard/handlers_pages.go`: `if mode == "admin"` → `if mode == "relay"`, and `s.renderPage(w, "admin", …)` → `s.renderPage(w, "relay", …)` (the template name must match the renamed file — confirm how `renderPage` resolves names, e.g. `pages/<name>.html`).

- [ ] **Step 3: Verify build + templates embed**

Run: `go build ./... && go vet ./internal/dashboard/`
Expected: clean. If templates are `embed`ed by glob, the rename is picked up automatically; if any name is referenced as a literal string elsewhere, grep `rg -n 'admin' internal/dashboard/` and fix.

- [ ] **Step 4: Headless render smoke (optional but recommended)**

If a scratch `TW_CONFIG_DIR` with `mode: relay` can be stood up, run the dashboard and `curl` the relay page returns 200. Otherwise rely on the e2e Dashboard/Contexts scenarios in Task 11.

---

### Task 4: `internal/ops/modeauth` package (TDD)

**Files:**
- Create: `internal/ops/modeauth/modeauth.go`
- Create: `internal/ops/modeauth/modeauth_test.go`

**Interfaces:**
- Produces:
  - `func Payload(mode, identity string) []byte` — canonical `tw-mode-v1\n<mode>\n<identity>`.
  - `func Sign(privPEM []byte, mode, identity string) (sigB64, issuerB64 string, err error)` — parses an OpenSSH ed25519 private key, signs `Payload`, returns base64 signature + base64 issuer public key (raw ed25519 32 bytes).
  - `func Verify(mode, identity, sigB64, issuerB64 string) error` — nil iff the signature is valid for the payload under the issuer key.
- The package is pure (no filesystem/network) and has no `tw` imports beyond stdlib + `crypto/ed25519` + `golang.org/x/crypto/ssh`.

- [ ] **Step 1: Write the failing test**

Create `internal/ops/modeauth/modeauth_test.go`:

```go
package modeauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// testKey returns a PEM-encoded OpenSSH ed25519 private key and its
// authorized-keys public form.
func testKey(t *testing.T) (privPEM []byte, pubAuthorized string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem_bytes(t, pemBlock), string(gossh.MarshalAuthorizedKey(sshPub))
}

func TestSignVerifyRoundTrip(t *testing.T) {
	priv, pub := testKey(t)
	sig, issuer, err := Sign(priv, "server", pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify("server", pub, sig, issuer); err != nil {
		t.Fatalf("round-trip verify failed: %v", err)
	}
}

func TestVerifyRejectsTamperedMode(t *testing.T) {
	priv, pub := testKey(t)
	sig, issuer, _ := Sign(priv, "server", pub)
	if err := Verify("relay", pub, sig, issuer); err == nil {
		t.Error("verify accepted a mode the signature does not cover")
	}
}

func TestVerifyRejectsTamperedIdentity(t *testing.T) {
	priv, pub := testKey(t)
	_, otherPub := testKey(t)
	sig, issuer, _ := Sign(priv, "client", pub)
	if err := Verify("client", otherPub, sig, issuer); err == nil {
		t.Error("verify accepted a different identity")
	}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	priv, pub := testKey(t)
	_, _ = base64.StdEncoding, ed25519.PublicKey(nil)
	sig, _, _ := Sign(priv, "server", pub)
	otherPriv, _ := testKey(t)
	_, wrongIssuer, _ := Sign(otherPriv, "server", pub)
	if err := Verify("server", pub, sig, wrongIssuer); err == nil {
		t.Error("verify accepted a signature under the wrong issuer key")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	if err := Verify("server", "id", "!!notb64!!", "!!notb64!!"); err == nil {
		t.Error("verify accepted garbage base64")
	}
}
```

Add the small PEM helper at the bottom of the test file:

```go
import "encoding/pem"

func pem_bytes(t *testing.T, b *pem.Block) []byte {
	t.Helper()
	return pem.EncodeToMemory(b)
}
```

(Consolidate the two `import` blocks into one when writing the file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ops/modeauth/`
Expected: FAIL — `undefined: Sign` / `Verify` / `Payload`.

- [ ] **Step 3: Implement**

Create `internal/ops/modeauth/modeauth.go`:

```go
// Package modeauth signs and verifies a profile's operating mode. The
// signature is TAMPER-EVIDENCE, not a security boundary: it makes tw refuse a
// role's commands when the config `mode` field was hand-edited, failing fast
// with a clear message. It does NOT stop a user who fully controls their
// machine (they can regenerate the whole profile) — that is inherently
// unpreventable client-side and gains no real power, because the real role
// boundary is the relay's authorized_keys (restrict vs shell) and the mTLS/PKI
// trust chain, which key possession — not this field — decides.
package modeauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"

	gossh "golang.org/x/crypto/ssh"
)

const payloadPrefix = "tw-mode-v1"

// Payload is the canonical signed message binding a mode to a profile
// identity (the profile's own id_ed25519.pub, trimmed).
func Payload(mode, identity string) []byte {
	return []byte(payloadPrefix + "\n" + mode + "\n" + identity)
}

// Sign signs Payload(mode, identity) with an OpenSSH ed25519 private key,
// returning base64 signature bytes and the base64 raw ed25519 public key.
func Sign(privPEM []byte, mode, identity string) (sigB64, issuerB64 string, err error) {
	signer, err := gossh.ParsePrivateKey(privPEM)
	if err != nil {
		return "", "", fmt.Errorf("parsing signer key: %w", err)
	}
	cpk, ok := signer.PublicKey().(gossh.CryptoPublicKey)
	if !ok {
		return "", "", errors.New("signer key is not an ed25519 key")
	}
	edPub, ok := cpk.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return "", "", errors.New("signer key is not ed25519")
	}
	sig, err := signer.Sign(nil, Payload(mode, identity))
	if err != nil {
		return "", "", fmt.Errorf("signing: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig.Blob),
		base64.StdEncoding.EncodeToString(edPub), nil
}

// Verify reports whether sigB64 is a valid signature over Payload(mode,
// identity) under the ed25519 public key issuerB64.
func Verify(mode, identity, sigB64, issuerB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("decoding signature: %w", err)
	}
	issuer, err := base64.StdEncoding.DecodeString(issuerB64)
	if err != nil {
		return fmt.Errorf("decoding issuer key: %w", err)
	}
	if len(issuer) != ed25519.PublicKeySize {
		return errors.New("issuer key is not a valid ed25519 public key")
	}
	if !ed25519.Verify(ed25519.PublicKey(issuer), Payload(mode, identity), sig) {
		return errors.New("mode signature does not verify")
	}
	return nil
}
```

Note: `signer.Sign` on an ed25519 OpenSSH signer produces a `*ssh.Signature` whose `Blob` is the raw 64-byte ed25519 signature — that is what `ed25519.Verify` expects. Confirm in Step 4; if the format differs, switch `Sign` to use `ed25519.Sign(privKey, Payload(...))` by extracting the raw private key via `gossh.ParseRawPrivateKey` (returns `*ed25519.PrivateKey`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ops/modeauth/ -v && go vet ./internal/ops/modeauth/`
Expected: all 5 tests PASS. If `TestSignVerifyRoundTrip` fails on signature format, apply the `ParseRawPrivateKey` fallback noted above and re-run.

---

### Task 5: `mode_auth` config field + relay self-sign & self-heal

**Files:**
- Modify: `internal/config/config.go` (add `ModeAuth` field + type)
- Create: `internal/ops/modeauth_wire.go` (ops-level helpers that read the profile key and stamp/verify mode_auth)
- Modify: `internal/ops/relay.go` (the two `cfg.Mode = "relay"` provisioning seams → also stamp mode_auth)
- Test: `internal/ops/modeauth_wire_test.go`

**Interfaces:**
- Consumes: `modeauth.Sign/Verify` (Task 4).
- Produces:
  - `config.ModeAuth{ Sig, Issuer string }` with yaml tags `sig`/`issuer`, field `ModeAuth *ModeAuth \`yaml:"mode_auth,omitempty"\`` on `Config`.
  - `func (o *Ops) stampModeAuth(cfg *config.Config) error` — signs the current canonical mode with the profile's own `id_ed25519`, sets `cfg.ModeAuth`. Used by Task 5 (relay), and reused by the relay self-heal.
  - `func profileIdentity() (string, error)` — reads `config.Dir()/id_ed25519.pub`, returns trimmed contents (the identity string). Used by stamp + Task 8 verify.
  - `func profilePrivPEM() ([]byte, error)` — reads `config.Dir()/id_ed25519`.

- [ ] **Step 1: Write the failing test**

Create `internal/ops/modeauth_wire_test.go`:

```go
package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

func writeProfileKey(t *testing.T) {
	t.Helper()
	priv, pub, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "id_ed25519"), priv, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "id_ed25519.pub"), pub, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStampModeAuthSignsCurrentMode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t)
	o, _ := New()
	cfg := o.Config()
	cfg.Mode = "relay"
	if err := o.stampModeAuth(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ModeAuth == nil || cfg.ModeAuth.Sig == "" {
		t.Fatal("stampModeAuth did not set a signature")
	}
	id, _ := profileIdentity()
	if err := modeauth.Verify("relay", id, cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err != nil {
		t.Errorf("stamped signature does not verify: %v", err)
	}
	// A different mode must NOT verify against the stamped signature.
	if err := modeauth.Verify("server", id, cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err == nil {
		t.Error("signature verified for the wrong mode")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ops/ -run TestStampModeAuth`
Expected: FAIL — `undefined: stampModeAuth` / `profileIdentity` / `cfg.ModeAuth`.

- [ ] **Step 3: Implement the config field + ops helpers**

In `internal/config/config.go`, add after `AnalyticsConfig` field on `Config`:

```go
	ModeAuth  *ModeAuth       `yaml:"mode_auth,omitempty"`
```
and the type:
```go
// ModeAuth is a detached signature over the profile's (mode, identity),
// making the mode field tamper-evident. See internal/ops/modeauth.
type ModeAuth struct {
	Sig    string `yaml:"sig"`
	Issuer string `yaml:"issuer"`
}
```

Create `internal/ops/modeauth_wire.go`:

```go
package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
)

// profileIdentity returns this profile's identity string for mode signing:
// the trimmed contents of id_ed25519.pub.
func profileIdentity() (string, error) {
	b, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		return "", fmt.Errorf("reading profile identity: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func profilePrivPEM() ([]byte, error) {
	return os.ReadFile(filepath.Join(config.Dir(), "id_ed25519"))
}

// stampModeAuth signs the config's current (canonical) mode with this
// profile's own key and stores the result in cfg.ModeAuth. Used where a
// profile self-signs its mode (the relay).
func (o *Ops) stampModeAuth(cfg *config.Config) error {
	priv, err := profilePrivPEM()
	if err != nil {
		return fmt.Errorf("reading profile key: %w", err)
	}
	id, err := profileIdentity()
	if err != nil {
		return err
	}
	sig, issuer, err := modeauth.Sign(priv, config.CanonicalMode(cfg.Mode), id)
	if err != nil {
		return err
	}
	cfg.ModeAuth = &config.ModeAuth{Sig: sig, Issuer: issuer}
	return nil
}
```

- [ ] **Step 4: Wire relay self-sign into provisioning**

In `internal/ops/relay.go`, at each of the two seams where `cfg.Mode = "relay"` is stamped (the `ProvisionRelay` step and `GenerateManualInstallScript`), immediately after setting the mode and before the `config.Save`, call `o.stampModeAuth(cfg)` (log and continue on error — never block provisioning on signing). Ensure the key exists at that point (provisioning runs after `EnsureKeys`); if not, move the stamp to just after key generation.

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/ops/ -run TestStampModeAuth && go build ./... && go vet ./internal/ops/ ./internal/config/`
Expected: PASS; build/vet clean.

---

### Task 6: Relay signs the server in the join response

**Files:**
- Modify: `internal/ops/join.go` (add `ModeSig`/`ModeIssuer` fields to `JoinResponse`; `ApplyJoinResponse` writes `mode_auth`)
- Modify: `internal/ops/enroll.go` (EnrollServer signs `(server, <req.SSHPubkey>)` and sets the response fields)
- Test: `internal/ops/enroll_test.go` (add a signing assertion)

**Interfaces:**
- Consumes: `modeauth.Sign` (Task 4), `profilePrivPEM` (Task 5), `req.SSHPubkey` (the server's identity, already in `JoinRequest`).
- Produces: `JoinResponse.ModeSig`, `JoinResponse.ModeIssuer` (json `mode_sig`/`mode_issuer`, additive, version unchanged). `ApplyJoinResponse` persists them into `cfg.ModeAuth`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ops/enroll_test.go`:

```go
func TestEnrollServerSignsServerMode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t) // relay's own key (helper from modeauth_wire_test.go)
	// Minimal relay config so EnrollServer can render (relay host + a UUID).
	o, _ := New()
	// The join request carries the SERVER's identity pubkey; reuse a fresh key.
	_, serverPub, _ := twssh.GenerateKeyPair()
	req := &JoinRequest{Version: 1, ServerID: "srv-1", UUID: "u-srv",
		Hostname: "srv", RelayHost: "relay.example", CACertPEM: testCAPEM(t),
		SSHPubkey: strings.TrimSpace(string(serverPub))}
	// Sign only — call the signing helper EnrollServer uses, not the full
	// SSH flow. If EnrollServer cannot be unit-run without a relay, assert on
	// the extracted helper signServerMode(req) instead (see Step 3).
	resp, sig, issuer := signServerModeForTest(t, o, req)
	_ = resp
	if err := modeauth.Verify("server", req.SSHPubkey, sig, issuer); err != nil {
		t.Fatalf("relay-signed server token does not verify: %v", err)
	}
}
```

Because `EnrollServer` needs a live relay (SSH), factor the signing into a pure helper and test that:

```go
func signServerModeForTest(t *testing.T, o *Ops, req *JoinRequest) (*JoinResponse, string, string) {
	t.Helper()
	sig, issuer, err := o.signServerMode(req)
	if err != nil {
		t.Fatal(err)
	}
	return &JoinResponse{ServerID: req.ServerID, ModeSig: sig, ModeIssuer: issuer}, sig, issuer
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ops/ -run TestEnrollServerSignsServerMode`
Expected: FAIL — `undefined: o.signServerMode`, `JoinResponse.ModeSig`.

- [ ] **Step 3: Implement**

In `internal/ops/join.go`, add to `JoinResponse`:

```go
	ModeSig    string `json:"mode_sig,omitempty"`
	ModeIssuer string `json:"mode_issuer,omitempty"`
```

Add a signing helper (in `enroll.go` or `join.go`):

```go
// signServerMode signs (mode=server, identity=<server's SSH pubkey>) with the
// relay's own key, for the join response. The server's identity comes from the
// join request, binding the token to that server's keypair.
func (o *Ops) signServerMode(req *JoinRequest) (sig, issuer string, err error) {
	priv, err := profilePrivPEM()
	if err != nil {
		return "", "", fmt.Errorf("reading relay key: %w", err)
	}
	return modeauth.Sign(priv, "server", strings.TrimSpace(req.SSHPubkey))
}
```

In `EnrollServer`, when building the returned `*JoinResponse` (the final `return &JoinResponse{...}`), call `signServerMode(req)` and set `ModeSig`/`ModeIssuer`. On signing error, log and return the response unsigned (legacy-tolerated) rather than failing the enroll.

In `ApplyJoinResponse`, after the existing `SetServerSettings`, persist the mode_auth when present:

```go
	if r.ModeSig != "" && r.ModeIssuer != "" {
		o.mu.Lock()
		o.cfg.ModeAuth = &config.ModeAuth{Sig: r.ModeSig, Issuer: r.ModeIssuer}
		cfg := o.cfg
		o.mu.Unlock()
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("persisting mode signature: %w", err)
		}
	}
```

Ensure `strings` and `config`/`modeauth` are imported where used.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ops/ -run 'TestEnrollServerSignsServerMode|TestStampModeAuth' && go build ./... && go vet ./internal/ops/`
Expected: PASS; build/vet clean.

---

### Task 7: Server signs the client in the user bundle

**Files:**
- Modify: `internal/ops/user.go` (`GetUserConfigBundle` ~781: inject `mode_auth` signed by the server, using the user's own pubkey as identity)
- Test: `internal/ops/user_test.go` or a new `internal/ops/userbundle_modeauth_test.go`

**Interfaces:**
- Consumes: `modeauth.Sign` (Task 4), `profilePrivPEM` (Task 5, the server's key), the user's `id_ed25519.pub` under `config.UsersDir()/<name>/`.
- Produces: the bundle's `config.yaml` carries a `mode_auth` block signed by the server over `(client, <user pubkey>)`.

- [ ] **Step 1: Write the failing test**

Create `internal/ops/userbundle_modeauth_test.go`:

```go
package ops

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	"gopkg.in/yaml.v3"
)

func TestUserBundleCarriesClientModeSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t) // the SERVER's own key
	o, _ := New()
	if err := o.SetMode("server"); err != nil {
		t.Fatal(err)
	}
	// Create a user so GetUserConfigBundle has something to export.
	if _, err := o.CreateUser("alice", nil, false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	bundle, err := o.GetUserConfigBundle("alice")
	if err != nil {
		t.Fatal(err)
	}
	// Unseal (empty passphrase) → unzip → read config.yaml.
	plain, err := cryptobox.Decrypt(bundle, "")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		t.Fatal(err)
	}
	var cfgYAML, userPub []byte
	for _, f := range zr.File {
		rc, _ := f.Open()
		b := new(bytes.Buffer)
		b.ReadFrom(rc)
		rc.Close()
		switch f.Name {
		case "config.yaml":
			cfgYAML = b.Bytes()
		case "id_ed25519.pub":
			userPub = b.Bytes()
		}
	}
	var cfg config.Config
	if err := yaml.Unmarshal(cfgYAML, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "client" || cfg.ModeAuth == nil {
		t.Fatalf("bundle config missing client mode_auth: mode=%q auth=%v", cfg.Mode, cfg.ModeAuth)
	}
	if err := modeauth.Verify("client", strings.TrimSpace(string(userPub)), cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err != nil {
		t.Errorf("client mode signature does not verify: %v", err)
	}
}
```

(If `CreateUser`'s signature differs, adjust the call — check `internal/ops/user.go` for the exact params; the test's intent is "a user exists".)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ops/ -run TestUserBundleCarriesClientModeSignature`
Expected: FAIL — `cfg.ModeAuth` is nil (bundle only injects `mode: client`).

- [ ] **Step 3: Implement**

In `GetUserConfigBundle`, where it currently does `clientCfg, err := injectMode(userCfg, "client")`, extend to also inject `mode_auth`. Add a helper next to `injectMode` in `user.go`:

```go
// injectClientModeAuth signs (client, <user pubkey>) with the server's own key
// and writes a mode_auth block into the user's client config.yaml, making the
// exported client's mode tamper-evident. Best-effort: on any signing error the
// bundle is emitted without a signature (legacy-tolerated on import).
func injectClientModeAuth(cfgYAML, userPubAuthorized []byte) ([]byte, error) {
	priv, err := profilePrivPEM()
	if err != nil {
		return cfgYAML, nil
	}
	sig, issuer, err := modeauth.Sign(priv, "client", strings.TrimSpace(string(userPubAuthorized)))
	if err != nil {
		return cfgYAML, nil
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal(cfgYAML, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	m["mode_auth"] = map[string]string{"sig": sig, "issuer": issuer}
	return yaml.Marshal(m)
}
```

In `GetUserConfigBundle`, read the user's pubkey (the code already handles `id_ed25519.pub` for the user at `filepath.Join(userDir, "id_ed25519.pub")`) and, after `injectMode`, pass the result through `injectClientModeAuth` before writing `config.yaml` into the zip. Import `modeauth` in `user.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ops/ -run TestUserBundleCarriesClientModeSignature && go build ./... && go vet ./internal/ops/`
Expected: PASS; build/vet clean.

---

### Task 8: Verify mode_auth in `requireMode` (+ relay self-heal)

**Files:**
- Modify: `internal/cli/root.go` (`requireMode` ~77 adds a verification step)
- Test: `internal/cli/root_test.go` (add verification cases)

**Interfaces:**
- Consumes: `config.Load` (canonical mode + `ModeAuth`), `profileIdentity` + `modeauth.Verify` (Tasks 4/5), `stampModeAuth` for relay self-heal (Task 5).
- Produces: `requireMode` refuses when `mode_auth` is present but invalid; warns once when absent (legacy); relay with a set mode but missing `mode_auth` self-heals (re-signs) instead of warning forever.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/root_test.go`:

```go
func TestRequireModeRejectsTamperedSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	// A profile that claims mode=server with a signature that covers a
	// DIFFERENT identity/mode must be refused.
	writeCLIProfileKey(t)
	id := readCLIIdentity(t)
	// Sign for "client", then claim "server" on disk.
	priv := readCLIPriv(t)
	sig, issuer, _ := modeauth.Sign(priv, "client", id)
	writeCLIConfig(t, "server", sig, issuer)
	if err := requireMode("server"); err == nil {
		t.Error("requireMode accepted a signature that does not cover mode=server")
	}
}

func TestRequireModeAllowsValidSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeCLIProfileKey(t)
	id := readCLIIdentity(t)
	priv := readCLIPriv(t)
	sig, issuer, _ := modeauth.Sign(priv, "server", id)
	writeCLIConfig(t, "server", sig, issuer)
	if err := requireMode("server"); err != nil {
		t.Errorf("requireMode rejected a valid signature: %v", err)
	}
}

func TestRequireModeAllowsUnsignedLegacy(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeCLIProfileKey(t)
	writeCLIConfig(t, "server", "", "") // no mode_auth
	if err := requireMode("server"); err != nil {
		t.Errorf("legacy unsigned profile should be tolerated: %v", err)
	}
}
```

Add these CLI test helpers to `root_test.go` (imports: `os`, `path/filepath`, `strings`, `github.com/tunnelwhisperer/tw/internal/config`, `twssh "github.com/tunnelwhisperer/tw/internal/ssh"`, `"gopkg.in/yaml.v3"`):

```go
func writeCLIProfileKey(t *testing.T) {
	t.Helper()
	priv, pub, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(config.Dir(), "id_ed25519"), priv, 0o600)
	os.WriteFile(filepath.Join(config.Dir(), "id_ed25519.pub"), pub, 0o644)
}

func readCLIIdentity(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func readCLIPriv(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeCLIConfig(t *testing.T, mode, sig, issuer string) {
	t.Helper()
	cfg := &config.Config{Mode: mode}
	if sig != "" {
		cfg.ModeAuth = &config.ModeAuth{Sig: sig, Issuer: issuer}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.FilePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
```

Import `modeauth` (`github.com/tunnelwhisperer/tw/internal/ops/modeauth`) in `root_test.go` too.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestRequireMode`
Expected: FAIL — `requireMode` does not yet verify signatures, so the tampered case is wrongly accepted.

- [ ] **Step 3: Implement**

In `internal/cli/root.go`, extend `requireMode` after the existing `modeError` check:

```go
func requireMode(allowed ...string) error {
	cfg, err := config.Load()
	if err != nil {
		return nil // can't determine mode, let the command proceed
	}
	if err := modeError(cfg.Mode, allowed); err != nil {
		return err
	}
	return verifyModeAuth(cfg)
}

// verifyModeAuth enforces the tamper-evidence signature over cfg.Mode. An
// unset mode is the bootstrap state and needs no signature. A present but
// invalid signature is refused. A missing signature on a set mode is
// legacy-tolerated with a one-time warning — except the relay, which holds
// its own signing key and self-heals by re-signing. See internal/ops/modeauth:
// this is tamper-evidence, not a security wall.
func verifyModeAuth(cfg *config.Config) error {
	if cfg.Mode == "" {
		return nil
	}
	id, err := ops.ProfileIdentity()
	if err != nil {
		return nil // no identity yet (pre-setup) — nothing to verify
	}
	if cfg.ModeAuth != nil {
		if err := modeauth.Verify(cfg.Mode, id, cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err != nil {
			return fmt.Errorf("mode signature invalid — the 'mode' field was modified or the profile is inconsistent; re-enroll (server) / re-import (client) / re-create (relay)")
		}
		return nil
	}
	// Missing signature: relay self-heals; others are legacy-tolerated.
	if cfg.Mode == "relay" {
		if o, err := ops.New(); err == nil {
			_ = o.StampAndSaveModeAuth() // best-effort re-sign
		}
		return nil
	}
	slog.Warn("mode is unsigned; re-enroll (server) or re-import (client) to sign it", "mode", cfg.Mode)
	return nil
}
```

Because `requireMode` is in package `cli` and the signing helpers are in `ops`, export thin wrappers from `ops`: `func ProfileIdentity() (string, error) { return profileIdentity() }` and `func (o *Ops) StampAndSaveModeAuth() error` (calls `stampModeAuth` on a loaded cfg then `config.Save`). Add these to `internal/ops/modeauth_wire.go`. Import `modeauth`, `ops`, `slog`, `fmt` in `root.go` as needed.

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/cli/ -run TestRequireMode && go test ./internal/cli/ && go build ./... && go vet ./internal/cli/ ./internal/ops/`
Expected: the three new tests PASS; existing cli tests still PASS; build/vet clean.

---

### Task 9: Close CLI gating gaps + fix export-user description

**Files:**
- Modify: `internal/cli/server_join.go` (`runServerJoin` → add gate)
- Modify: `internal/cli/client.go` (`runClientListen` → add gate)
- Modify: `internal/cli/proxy.go` (`runProxySet`/`runProxyClear`/`runProxyShow` → add gate)
- Modify: `internal/cli/export_user.go:16` (Short reword)
- Modify: `e2e/coverage.yaml` (map any newly-gated command paths)
- Test: `internal/cli/root_test.go` or command-specific — assert the gate via `modeError` where practical

**Interfaces:**
- Consumes: `requireMode` (now signature-aware, Task 8).
- Produces: `tw server join-relay` (server), `tw client listen` (client), `tw proxy set/clear/show` (server or client) are gated; `export-user` Short no longer claims encryption.

- [ ] **Step 1: Add the gates**

In `runServerJoin` (`internal/cli/server_join.go`), as the first statement:
```go
	if err := requireMode("server"); err != nil {
		return err
	}
```
In `runClientListen` (`internal/cli/client.go`):
```go
	if err := requireMode("client"); err != nil {
		return err
	}
```
In `runProxySet`, `runProxyClear`, `runProxyShow` (`internal/cli/proxy.go`), each as the first statement:
```go
	if err := requireMode("server", "client"); err != nil {
		return err
	}
```

- [ ] **Step 2: Fix the export-user description**

In `internal/cli/export_user.go`, change the `Short`:
```go
	Short: "Issue a user as a client context bundle (server only; unprotected — send over a trusted channel)",
```

- [ ] **Step 3: Map coverage for the newly-gated commands**

`internal/cli/coverage_test.go` keys on command paths, not on gating, so already-mapped commands (`server join-relay`, `proxy …`, `client listen`) need no new entry unless they were previously unmapped. Run the gate to see:
Run: `go test ./internal/cli/ -run TestEveryCommandHasE2ECoverage`
If it fails for any of these paths, add an entry in `e2e/coverage.yaml` mapping to the scenario that exercises it (e.g. `"server join-relay"` already maps to ServerJoin; `"client listen"` / `"proxy set"` may need `{exempt: "..."}` or a real scenario — prefer mapping `client listen` to the Contexts/client path if exercised, else a documented exempt).

- [ ] **Step 4: Verify**

Run: `go build ./... && go vet ./internal/cli/ && go test ./internal/cli/`
Expected: build/vet clean; coverage gate green; existing tests pass.

- [ ] **Step 5: Smoke the gates**

Run:
```bash
go run ./cmd/tw --help >/dev/null   # sanity: tree still builds/loads
```
And confirm the reworded help: `go run ./cmd/tw config export-user --help` shows the new Short without "encrypted".

---

### Task 10: e2e — mode rename, signed mode, gating

**Files:**
- Modify: `e2e/e2e_test.go` / `e2e/server_test.go` / `e2e/users_test.go` (extend existing scenarios; no new topology)
- Modify: `e2e/coverage.yaml` if Task 9 flagged gaps

**Interfaces:**
- Consumes: harness helpers `execIn`/`execInOK`/`fatalf`/`scenario` (`e2e/harness.go`). The relay/admin container is compose service `admin`; server `server`; client work in `UserLifecycle`/`Contexts`.

- [ ] **Step 1: Assert the relay role name + a signed relay mode**

In the Contexts scenario (which already asserts the relay profile's role), ensure the expected ROLE is now `relay` (update the regex from `admin` to `relay`). Add: on the `admin` container, `tw config view` (or read the active config) shows a `mode_auth:` block. A simple assertion:
```go
out := execIn(t, "admin", "tw config view")
if !strings.Contains(out, "mode_auth:") {
	fatalf(t, "relay profile is not mode-signed:\n%s", out)
}
```

- [ ] **Step 2: Assert tampering the server mode is refused**

In `ServerJoin` (after `--apply` succeeds and the server config has `mode: server`), add a negative check that editing mode to `relay` breaks the gate:
```go
// Tamper: flip mode server→relay in the active config; a relay command must
// now fail the signature gate (tamper-evidence).
execIn(t, "server", `sed -i 's/^mode: server/mode: relay/' $(tw config path 2>/dev/null || echo /etc/tw-test/*/config.yaml)`)
if out, err := execInOK("server", "tw relay get-servers"); err == nil {
	fatalf(t, "relay command succeeded on a tampered server profile:\n%s", out)
} else if !strings.Contains(out, "mode signature invalid") {
	fatalf(t, "expected a mode-signature error, got:\n%s", out)
}
// Restore so later scenarios are unaffected.
execIn(t, "server", `sed -i 's/^mode: relay/mode: server/' $(tw config path 2>/dev/null || echo /etc/tw-test/*/config.yaml)`)
```
(Confirm how the e2e locates the active config path; if there is no `tw config path`, use the known `TW_CONFIG_DIR` the harness sets — check `e2e/harness.go` for the env it exports and hardcode that path.)

- [ ] **Step 3: Assert a client cannot run server/relay ops**

In `UserLifecycle` (which imports a client bundle), after the client context is active, add:
```go
if out, err := execInOK("client", "tw server join-relay relay.example"); err == nil {
	fatalf(t, "client profile was allowed to run a server command:\n%s", out)
} else if !strings.Contains(out, "requires server mode") {
	fatalf(t, "expected a server-mode gate error, got:\n%s", out)
}
```
And that the imported client bundle is signed:
```go
if !strings.Contains(execIn(t, "client", "tw config view"), "mode_auth:") {
	fatalf(t, "imported client profile is not mode-signed")
}
```

- [ ] **Step 4: Run the full e2e suite**

Run: `go vet -tags e2e ./e2e/ && make e2e`
Expected: PASS (Smoke, RelayInstall, ServerJoin, MTLSGate, UserLifecycle, PermitOpen, Revocation, Contexts, SecondTenant; Dashboard/RelayResilience/Teardown SKIP). If a scenario fails because the mode gate now blocks a step that legitimately ran cross-role before, that is a real finding — fix the *scenario* to use the correct role/context, not the product gate. If a PRODUCT gate wrongly blocks a legitimate same-role op, STOP and report it.

---

### Task 11: Wrap-up — verify, bins, session notes

**Files:**
- Modify: `.claude/session-history.md`

- [ ] **Step 1: Full verification sweep**

Run: `go build ./... && go vet ./... && go test ./internal/config/ ./internal/ops/ ./internal/ops/modeauth/ ./internal/cli/ ./internal/pki/ ./internal/relay/caddy/`
Expected: all clean/PASS. (`make e2e` already passed in Task 10.)

- [ ] **Step 2: Confirm no canonical `admin` leaked back**

Run: `rg -n '"admin"' internal/ --type go | grep -v _test`
Expected: no matches except genuine human-administrator wording. `rg -n 'mode: admin|role: admin' docs/ internal/` should only appear in migration/spec context.

- [ ] **Step 3: Build both binaries and stage tw.exe**

Run:
```bash
make build && make build-windows
cp bin/tw.exe /mnt/c/Users/alial/Downloads/tw.exe
```
Expected: `bin/tw` and `bin/tw.exe` built; staging succeeds.

- [ ] **Step 4: Update session history**

Prepend a dated block to `.claude/session-history.md`: the admin→relay rename + migration, the `mode_auth` tamper-evidence design (issuer chain relay→server→client, verified in requireMode, legacy-tolerated, relay self-heal), the closed gating gaps, the export-user description fix, e2e coverage, and that the work is uncommitted (user drives git).

- [ ] **Step 5: Report**

Tell the user: feature done, verified (unit + full e2e outputs), bins staged, NOT committed — awaiting their instruction. Note explicitly that the mode signature is tamper-evidence layered on the unchanged key/PKI boundary.
