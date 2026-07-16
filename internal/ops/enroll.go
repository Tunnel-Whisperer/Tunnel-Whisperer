package ops

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/relay/caddy"
	relayxray "github.com/tunnelwhisperer/tw/internal/relay/xray"
	gossh "golang.org/x/crypto/ssh"
)

// EnrollServer registers a joining server onto the admin's relay non-disruptively:
//  1. AddServer — allocates a RemotePort and records the server in the registry.
//  2. Builds the FULL tenant list: the admin's own entry + every registered server.
//  3. Over RelaySSH:
//     - writes each CA cert to /etc/caddy/ca/<id>.crt
//     - re-renders + validates + reloads the Caddyfile (graceful; no xray restart)
//     - appends the new server's SSH pubkey to authorized_keys idempotently
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

	osHost, _ := os.Hostname()
	adminID := deriveServerID(osHost, cfg.Xray.UUID)
	adminRemotePort := cfg.Server.RemotePort

	// Read admin's own CA cert.
	adminCAPEM, err := os.ReadFile(config.CACertPath())
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("reading admin CA cert: %w", err)
	}

	// Seed with the admin's own entry.
	servers := []caddy.Server{{
		ID:         adminID,
		Path:       "/tw/" + adminID,
		CACertPath: fmt.Sprintf("/etc/caddy/ca/%s.crt", adminID),
		Upstream:   fmt.Sprintf("h2c://127.0.0.1:%d", adminRemotePort+10000),
		Role:       "admin",
	}}
	tenants := []relayxray.Tenant{{
		ServerID:   adminID,
		UUID:       cfg.Xray.UUID,
		RemotePort: adminRemotePort,
	}}
	caCerts := map[string][]byte{
		adminID: adminCAPEM,
	}

	// All registered servers (includes the one just added).
	allServers, err := o.ListServers()
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Build tenant list", Status: "failed", Error: err.Error()})
		return nil, fmt.Errorf("listing registered servers: %w", err)
	}
	for _, s := range allServers {
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

		// Append the new server's SSH pubkey, restricted: a tenant may ONLY
		// establish its reverse forward on its own port — no shell, no exec, no
		// sudo (management stays admin-only) — and only through the tunnel
		// (from="127.0.0.1"). Strip any prior line for this key first so a
		// re-enroll can't leave an unrestricted copy behind.
		akPath := fmt.Sprintf("/home/%s/.ssh/authorized_keys", sshUser)
		keyBody := req.SSHPubkey
		if f := strings.Fields(req.SSHPubkey); len(f) >= 2 {
			keyBody = f[1] // the base64 key material — no shell/regex metachars
		}
		// restrict = no shell/exec/sudo/agent/x11 and no forwarding; port-forwarding
		// re-enables forwarding (restrict alone denies the tcpip-forward request);
		// permitlisten limits the -R reverse forward to this tenant's own port;
		// permitopen pins local (-L) forwarding to a dead sentinel port so a tenant
		// can't reach the relay's Xray gRPC API (127.0.0.1:10085) or other loopback
		// ports. The admin key is unrestricted, so admin -L (for gRPC) still works.
		line := fmt.Sprintf(`from="127.0.0.1",restrict,port-forwarding,permitopen="127.0.0.1:1",permitlisten="127.0.0.1:%d" %s`,
			newServer.RemotePort, req.SSHPubkey)
		lineB64 := base64.StdEncoding.EncodeToString([]byte(line))
		writeKey := fmt.Sprintf(
			"sudo touch %s && sudo sed -i '\\#%s#d' %s && echo %s | base64 -d | sudo tee -a %s >/dev/null",
			akPath, keyBody, akPath, lineB64, akPath,
		)
		if err := runRelayCmd(client, writeKey); err != nil {
			return fmt.Errorf("appending ssh pubkey: %w", err)
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
