package ops

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/relay/caddy"
	relayxray "github.com/tunnelwhisperer/tw/internal/relay/xray"
	gossh "golang.org/x/crypto/ssh"
)

// renderRelayAuthorizedKeys composes the relay's tw-managed authorized_keys
// file: the admin's tunnel-only key first, then one forward-only line per
// enrolled server. Every line is newline-terminated and the file is fully
// rewritten on each enroll (same philosophy as the Caddyfile and the relay
// config.json), so a stale or corrupted file self-heals.
//
// Server lines: restrict = no shell/exec/sudo/agent/x11 and no forwarding;
// port-forwarding re-enables forwarding (restrict alone denies the
// tcpip-forward request); permitlisten limits the -R reverse forward to this
// tenant's own port; permitopen pins local (-L) forwarding to a dead sentinel
// port so a tenant can't reach the relay's Xray gRPC API (127.0.0.1:10085) or
// other loopback ports. The admin key is unrestricted (beyond tunnel-only
// from=), so admin -L (for gRPC) still works.
func renderRelayAuthorizedKeys(adminPubKey string, servers []RegisteredServer) string {
	lines := []string{fmt.Sprintf(`from="127.0.0.1" %s`, strings.TrimSpace(adminPubKey))}
	for _, s := range servers {
		lines = append(lines, fmt.Sprintf(
			`from="127.0.0.1",restrict,port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:%d" %s`,
			s.RemotePort, strings.TrimSpace(s.SSHPubkey)))
	}
	return strings.Join(lines, "\n") + "\n"
}

// relayTenantState builds the relay-side tenant state — Caddy server blocks,
// xray tenants, and the CA certs to install — from the admin's own entry
// plus the given registered servers.
func (o *Ops) relayTenantState(registered []RegisteredServer) ([]caddy.Server, []relayxray.Tenant, map[string][]byte, error) {
	cfg := o.Config()
	osHost, _ := os.Hostname()
	adminID := deriveServerID(osHost, cfg.Xray.UUID)
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
//     - rewrites authorized_keys in full (admin key + every tenant's key)
//     - writes the full Xray config.json for persistence
//     - calls liveAddTenant for only the new tenant (no xray restart)
//  4. Returns a JoinResponse with the admin-assigned coordinates.
func (o *Ops) EnrollServer(req *JoinRequest, progress ProgressFunc) (*JoinResponse, error) {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

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
	akContent := renderRelayAuthorizedKeys(string(adminPubKey), allServers)

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

		// Rewrite the relay's authorized_keys in full from the tenant list —
		// same philosophy as the Caddyfile and config.json above. A previous
		// version APPENDED the new line without a trailing newline, so the
		// second enroll glued its line onto the first server's, corrupting
		// both entries (sshd rejects the merged line). Rewriting is idempotent
		// and self-heals such a file. The file is tw-managed: admin key first,
		// then one line per enrolled server.
		akPath := fmt.Sprintf("/home/%s/.ssh/authorized_keys", sshUser)
		akB64 := base64.StdEncoding.EncodeToString([]byte(akContent))
		writeKeys := fmt.Sprintf(
			"echo %s | base64 -d | sudo tee %s >/dev/null && sudo chown %s:%s %s && sudo chmod 600 %s",
			akB64, akPath, sshUser, sshUser, akPath, akPath,
		)
		if err := runRelayCmd(client, writeKeys); err != nil {
			return fmt.Errorf("writing authorized_keys: %w", err)
		}

		// Write the full Xray config.json for persistence (survives xray restarts/reboots).
		// Do NOT restart xray — liveAddTenant handles the live update below.
		xB64 := base64.StdEncoding.EncodeToString([]byte(xjson))
		if err := runRelayCmd(client, fmt.Sprintf("echo %s | base64 -d | sudo tee /usr/local/etc/xray/config.json >/dev/null", xB64)); err != nil {
			return fmt.Errorf("writing relay Xray config: %w", err)
		}

		return nil
	})
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("applying relay config: %w", err)
	}
	progress(ProgressEvent{Step: 3, Total: total, Label: "Apply relay config", Status: "completed",
		Message: "Caddyfile reloaded, pubkey appended, config.json written"})

	// Step 4: Live-add only the new tenant (existing tenants are already running).
	progress(ProgressEvent{Step: 4, Total: total, Label: "Live enroll tenant", Status: "running"})
	if err := o.RelaySSH(func(client *gossh.Client) error {
		return liveAddTenant(client, newTenant)
	}); err != nil {
		progress(ProgressEvent{Step: 4, Total: total, Label: "Live enroll tenant", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("live-enrolling tenant: %w", err)
	}
	progress(ProgressEvent{Step: 4, Total: total, Label: "Live enroll tenant", Status: "completed",
		Message: fmt.Sprintf("tenant %s live on port %d", newServer.ServerID, newServer.RemotePort)})

	// Step 5: Return the join response.
	progress(ProgressEvent{Step: 5, Total: total, Label: "Done", Status: "completed",
		Message: fmt.Sprintf("%s enrolled on relay %s", req.ServerID, cfg.Xray.RelayHost)})

	return &JoinResponse{
		Version:    1,
		ServerID:   req.ServerID,
		RelayHost:  cfg.Xray.RelayHost,
		Path:       "/tw/" + req.ServerID,
		RemotePort: newServer.RemotePort,
		SSHUser:    sshUser,
	}, nil
}
