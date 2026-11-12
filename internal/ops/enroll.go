package ops

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	"github.com/tunnelwhisperer/tw/internal/relay/caddy"
	relaytenant "github.com/tunnelwhisperer/tw/internal/relay/tenant"
	relayxray "github.com/tunnelwhisperer/tw/internal/relay/xray"
	gossh "golang.org/x/crypto/ssh"
)

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

// renderRelayAuthorizedKeys composes the relay admin account's tw-managed
// authorized_keys file: the admin's key only. Enrolled servers no longer log
// in as the admin (who has NOPASSWD sudo) — each gets its own tenant account
// (renderTenantAuthorizedKeys). The file is fully rewritten on every enroll,
// un-enroll and sync, which also drops tenant lines a pre-migration relay
// still carries here.
//
// sshOpen: the admin line is pinned from="127.0.0.1" (tunnel-only) UNLESS
// the relay was provisioned --ssh-open — the pin rejects any non-loopback
// source, which made the deliberately-open port 22 unusable with tw's own
// key.
func renderRelayAuthorizedKeys(adminPubKey string, sshOpen bool) string {
	// The admin key is the operator's own, locally-generated material; if it
	// somehow fails to canonicalize we fall back to a trim rather than lock the
	// operator out of their own relay.
	adminLine, err := canonicalAuthorizedKey(adminPubKey)
	if err != nil {
		slog.Warn("admin ssh key did not canonicalize; using trimmed form", "error", err)
		adminLine = strings.TrimSpace(adminPubKey)
	}
	if !sshOpen {
		adminLine = `from="127.0.0.1" ` + adminLine
	}
	return adminLine + "\n"
}

// renderTenantAuthorizedKeys renders the single authorized_keys line of an
// enrolled server's own relay account (relaytenant.UserName). OpenSSH
// "restrict" does not forbid exec or shell, so command= forces
// tw-tenant-request for every session: the tenant can only send requests
// about its own inbound to the root handler, never run anything.
// port-forwarding re-enables forwarding (restrict alone denies tcpip-forward);
// permitlisten limits -R to the tenant's own tunnel and enroll ports;
// permitopen pins -L to a dead sentinel port so the tenant can't reach the
// Xray gRPC API (127.0.0.1:10085) or other loopback ports. from= binds the key
// to the VLESS tunnel, which egresses at loopback.
func renderTenantAuthorizedKeys(s RegisteredServer) (string, error) {
	// A tenant key is attacker-supplied. Canonicalize it to a single,
	// option-free line so it cannot break out of its restriction prefix.
	keyLine, err := canonicalAuthorizedKey(s.SSHPubkey)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`from="127.0.0.1",restrict,command="%s",port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:%d",permitlisten="127.0.0.1:%d" %s`+"\n",
		relaytenant.RequestPath, s.RemotePort, enrollPort(s.RemotePort), keyLine), nil
}

// renderTenantSync renders the root script that installs the tenant request
// handler on the relay, reconciles one tenant account per registered
// server and then rewrites the admin account sshUser's authorized_keys with
// akContent (see relaytenant.RenderSync for why in that order). A server
// whose key fails to canonicalize gets no account (fail closed); registry
// entries written by AddServer are already canonical, so this only trips on
// pre-existing/corrupted state.
func renderTenantSync(sshUser, akContent string, servers []RegisteredServer) (string, error) {
	accounts := make([]relaytenant.Account, 0, len(servers))
	for _, s := range servers {
		ak, err := renderTenantAuthorizedKeys(s)
		if err != nil {
			slog.Warn("skipping tenant with invalid ssh key in relay tenant accounts",
				"server_id", s.ServerID, "error", err)
			continue
		}
		accounts = append(accounts, relaytenant.Account{
			ServerID:       s.ServerID,
			AuthorizedKeys: ak,
			Ports:          []int{s.RemotePort, enrollPort(s.RemotePort)},
		})
	}
	return relaytenant.RenderSync(&relaytenant.Admin{User: sshUser, AuthorizedKeys: akContent}, accounts)
}

// runRelayTenantSync runs a renderTenantSync script as root on the relay.
func runRelayTenantSync(client *gossh.Client, script string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("opening ssh session: %w", err)
	}
	defer session.Close()
	session.Stdin = strings.NewReader(script)
	out, err := session.CombinedOutput("sudo sh -s")
	if err != nil {
		return fmt.Errorf("syncing relay tenant accounts: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			slog.Info("relay tenant sync", "output", line)
		}
	}
	return nil
}

// relayTenantState builds the relay-side tenant state — Caddy server blocks,
// xray tenants, and the CA certs to install — from the admin's own entry
// plus the given registered servers.
func (o *Ops) relayTenantState(registered []RegisteredServer) ([]caddy.Server, []relayxray.Tenant, map[string][]byte, error) {
	cfg := o.Config()
	adminID, err := o.serverID()
	if err != nil {
		return nil, nil, nil, err
	}
	adminRemotePort := cfg.Server.RemotePort

	adminCAPEM, err := os.ReadFile(config.CACertPath())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading admin CA cert: %w", err)
	}

	servers := []caddy.Server{{
		ID:         adminID,
		Path:       "/tw/" + adminID,
		CACertPath: fmt.Sprintf("/etc/caddy/ca/%s.crt", adminID),
		Upstream:   fmt.Sprintf("h2c://127.0.0.1:%d", adminRemotePort+10000),
		Role:       "relay",
		EnrollTok:  first8(cfg.Xray.UUID),
		EnrollPort: enrollPort(adminRemotePort),
	}}
	tenants := []relayxray.Tenant{{
		ServerID:   adminID,
		UUID:       cfg.Xray.UUID,
		RemotePort: adminRemotePort,
	}}
	caCerts := map[string][]byte{adminID: adminCAPEM}

	for _, s := range registered {
		servers = append(servers, caddy.Server{
			ID:         s.ServerID,
			Path:       "/tw/" + s.ServerID,
			CACertPath: fmt.Sprintf("/etc/caddy/ca/%s.crt", s.ServerID),
			Upstream:   fmt.Sprintf("h2c://127.0.0.1:%d", s.RemotePort+10000),
			Role:       "server",
			EnrollTok:  first8(s.UUID),
			EnrollPort: enrollPort(s.RemotePort),
		})
		tenants = append(tenants, relayxray.Tenant{
			ServerID:   s.ServerID,
			UUID:       s.UUID,
			RemotePort: s.RemotePort,
		})
		caCerts[s.ServerID] = []byte(s.CACertPEM)
	}
	return servers, tenants, caCerts, nil
}

// EnrollServer registers a joining server onto the admin's relay non-disruptively:
//  1. AddServer — allocates a RemotePort and records the server in the registry.
//  2. Builds the FULL tenant list: the admin's own entry + every registered server.
//  3. Over RelaySSH:
//     - writes each CA cert to /etc/caddy/ca/<id>.crt
//     - re-renders + validates + reloads the Caddyfile (graceful; no xray restart)
//     - rewrites the admin account's authorized_keys (admin key only)
//     - installs the tenant request handler and gives every enrolled server
//     its own locked-down relay account (renderTenantSync)
//     - writes the full Xray config.json for persistence
//     - calls liveAddTenant for only the new tenant (no xray restart)
//  4. Returns a JoinResponse with the admin-assigned coordinates.
func (o *Ops) EnrollServer(req *JoinRequest, progress ProgressFunc) (_ *JoinResponse, err error) {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}
	// Enroll and un-enroll both render the FULL relay state from the registry
	// and rewrite it wholesale — interleaved runs would drop each other's
	// tenant, so they are serialized per admin profile.
	release, err := acquireEnrollLock()
	if err != nil {
		return nil, err
	}
	defer release()

	const total = 5

	// Step 1: Register the server and allocate its RemotePort.
	progress(ProgressEvent{Step: 1, Total: total, Label: "Register server", Status: "running"})
	newServer, err := o.AddServer(req)
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Register server", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("adding server to registry: %w", err)
	}
	progress(ProgressEvent{Step: 1, Total: total, Label: "Register server", Status: "completed",
		Message: fmt.Sprintf("allocated port %d for %s", newServer.RemotePort, newServer.ServerID)})

	// Until the relay is touched, a failure leaves nothing but the registry
	// entry, so drop it. From then on the relay may already carry the tenant
	// (its key, route and CA), and the entry is what un-enroll uses to remove
	// it again, so it stays.
	relayTouched := false
	defer func() {
		if err == nil || relayTouched {
			return
		}
		if rerr := os.Remove(filepath.Join(RegistryDir(), newServer.ServerID+".json")); rerr != nil {
			err = fmt.Errorf("%w (removing registry entry %s also failed: %v)", err, newServer.ServerID, rerr)
		}
	}()

	// Step 2: Build the full tenant list — admin + every registered server.
	progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "running"})
	cfg := o.Config()

	allServers, err := o.ListServers()
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("listing registered servers: %w", err)
	}
	servers, tenants, caCerts, err := o.relayTenantState(allServers)
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "failed", Error: err.Error()})
		return nil, err
	}

	newTenant := relayxray.Tenant{
		ServerID:   newServer.ServerID,
		UUID:       newServer.UUID,
		RemotePort: newServer.RemotePort,
	}

	progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "completed",
		Message: fmt.Sprintf("%d tenants (admin + %d servers)", len(tenants), len(allServers))})

	// Step 3: Render the Caddyfile (outside the SSH callback — fail fast).
	progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "running"})
	caddyfile, err := caddy.RenderCaddyfile(caddy.Config{
		Domain:  cfg.Xray.RelayHost,
		Servers: servers,
	})
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("rendering Caddyfile: %w", err)
	}

	xjson, err := relayxray.RenderConfig(relayxray.Config{Tenants: tenants})
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("rendering relay Xray config: %w", err)
	}

	sshUser := cfg.Server.RelaySSHUser

	adminPubKey, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("reading admin public key: %w", err)
	}
	akContent := renderRelayAuthorizedKeys(string(adminPubKey), o.GetRelayStatus().SSHOpen)
	tenantSync, err := renderTenantSync(sshUser, akContent, allServers)
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("rendering relay tenant accounts: %w", err)
	}

	relayTouched = true
	err = o.RelaySSH(func(client *gossh.Client) error {
		// Ensure CA directory exists.
		if err := runRelayCmd(client, "sudo mkdir -p /etc/caddy/ca"); err != nil {
			return err
		}

		// Write each CA cert.
		for id, pem := range caCerts {
			b64 := base64.StdEncoding.EncodeToString(pem)
			cmd := fmt.Sprintf("echo %s | base64 -d | sudo tee /etc/caddy/ca/%s.crt >/dev/null", b64, id)
			if err := runRelayCmd(client, cmd); err != nil {
				return fmt.Errorf("writing CA for %s: %w", id, err)
			}
		}

		// Write Caddyfile.
		cfB64 := base64.StdEncoding.EncodeToString([]byte(caddyfile))
		if err := runRelayCmd(client, fmt.Sprintf("echo %s | base64 -d | sudo tee /etc/caddy/Caddyfile >/dev/null", cfB64)); err != nil {
			return fmt.Errorf("writing Caddyfile: %w", err)
		}

		// Validate before reload (fail safe — don't reload a broken Caddyfile).
		if err := runRelayCmd(client, "sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile"); err != nil {
			return fmt.Errorf("relay Caddyfile failed validation (not reloaded): %w", err)
		}

		// Graceful reload — Caddy finishes in-flight requests.
		if err := runRelayCmd(client, "sudo systemctl reload caddy"); err != nil {
			return fmt.Errorf("reloading caddy: %w", err)
		}

		// Give every enrolled server — the new one included — its own
		// account, which can reach nothing but its reverse-tunnel ports and
		// the tenant request handler; then (same script) rewrite the admin
		// account's authorized_keys in full (admin key only; this also drops
		// the tenant lines a pre-migration relay kept there).
		if err := runRelayTenantSync(client, tenantSync); err != nil {
			return err
		}

		// Write the full Xray config.json for persistence (survives xray restarts/reboots).
		// The rendered tenant list knows only each server's own UUID; the
		// client-user UUIDs servers added to their inbounds live only in the
		// relay's current file, so carry them over before overwriting it.
		// Do NOT restart xray — liveAddTenant handles the live update below.
		return writeMergedRelayXrayConfig(client, xjson)
	})
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("applying relay config (tenant %s stays registered; remove it with 'tw relay un-enroll-server %s'): %w", newServer.ServerID, newServer.ServerID, err)
	}
	progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "completed",
		Message: "Caddyfile reloaded, tenant account ready, config.json written"})

	// Step 4: Live-add only the new tenant (existing tenants are already running).
	progress(ProgressEvent{Step: 4, Total: total, Label: "Live enroll tenant", Status: "running"})
	if err := o.RelaySSH(func(client *gossh.Client) error {
		return liveAddTenant(client, newTenant)
	}); err != nil {
		progress(ProgressEvent{Step: 4, Total: total, Label: "Live enroll tenant", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("live-enrolling tenant (tenant %s stays registered; remove it with 'tw relay un-enroll-server %s'): %w", newServer.ServerID, newServer.ServerID, err)
	}
	progress(ProgressEvent{Step: 4, Total: total, Label: "Live enroll tenant", Status: "completed",
		Message: fmt.Sprintf("tenant %s live on port %d", newServer.ServerID, newServer.RemotePort)})

	// Step 5: Return the join response.
	progress(ProgressEvent{Step: 5, Total: total, Label: "Done", Status: "completed",
		Message: fmt.Sprintf("%s enrolled on relay %s", req.ServerID, cfg.Xray.RelayHost)})

	resp := &JoinResponse{
		Version:    1,
		ServerID:   req.ServerID,
		RelayHost:  cfg.Xray.RelayHost,
		Path:       "/tw/" + req.ServerID,
		RemotePort: newServer.RemotePort,
		SSHUser:    relaytenant.UserName(newServer.ServerID),
	}
	// Sign the server's mode into the response so it can verify its own
	// identity later. Signing failure is legacy-tolerated: the enroll itself
	// already succeeded, so return the response unsigned rather than fail it.
	if sig, issuer, err := o.signServerMode(req); err != nil {
		slog.Warn("signing server mode for join response failed; returning unsigned", "server_id", req.ServerID, "error", err)
	} else {
		resp.ModeSig = sig
		resp.ModeIssuer = issuer
	}
	return resp, nil
}

// SyncRelayTenants brings the relay's tenant accounts in line with the
// registry without enrolling anyone: it rewrites the admin account's
// authorized_keys (admin key only), installs or updates the tenant request
// handler, and creates/updates/removes one relay account per registered
// server. This is how a relay provisioned before per-tenant accounts drops
// tenant keys from the admin account; enroll and un-enroll run the same sync.
func (o *Ops) SyncRelayTenants(progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}
	release, err := acquireEnrollLock()
	if err != nil {
		return err
	}
	defer release()
	const total = 2

	progress(ProgressEvent{Step: 1, Total: total, Label: "Render tenant accounts", Status: "running"})
	servers, err := o.ListServers()
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Render tenant accounts", Status: "failed", Error: err.Error()})
		return fmt.Errorf("listing registered servers: %w", err)
	}
	adminPubKey, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Render tenant accounts", Status: "failed", Error: err.Error()})
		return fmt.Errorf("reading admin public key: %w", err)
	}
	akContent := renderRelayAuthorizedKeys(string(adminPubKey), o.GetRelayStatus().SSHOpen)
	sshUser := o.Config().Server.RelaySSHUser
	script, err := renderTenantSync(sshUser, akContent, servers)
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Render tenant accounts", Status: "failed", Error: err.Error()})
		return fmt.Errorf("rendering relay tenant accounts: %w", err)
	}
	progress(ProgressEvent{Step: 1, Total: total, Label: "Render tenant accounts", Status: "completed",
		Message: fmt.Sprintf("%d enrolled servers", len(servers))})

	progress(ProgressEvent{Step: 2, Total: total, Label: "Apply on relay", Status: "running"})
	err = o.RelaySSH(func(client *gossh.Client) error {
		return runRelayTenantSync(client, script)
	})
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Apply on relay", Status: "failed", Error: err.Error()})
		return err
	}
	progress(ProgressEvent{Step: 2, Total: total, Label: "Apply on relay", Status: "completed",
		Message: "admin authorized_keys rewritten, tenant accounts in sync"})
	return nil
}
