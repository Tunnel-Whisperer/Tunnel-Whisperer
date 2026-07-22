package ops

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/relay/caddy"
	relayxray "github.com/tunnelwhisperer/tw/internal/relay/xray"
	gossh "golang.org/x/crypto/ssh"
)

// killRelayListenerCmd returns a shell command that kills whatever process
// holds a LISTEN socket on the given port on the relay — in practice the
// sshd session serving a tenant's reverse tunnel, which survives an
// authorized_keys rewrite. All tenants (and the admin's own management
// session) share one SSH user, so the kill targets the listener port, never
// a process name. Pure coreutils + awk (no ss/fuser): find the LISTEN
// socket inode in /proc/net/tcp{,6} (state 0A, hex port), then the pid
// whose fd table holds that inode. No listener found is success — the
// tunnel was already down — hence the trailing true.
func killRelayListenerCmd(port int) string {
	return fmt.Sprintf(
		`inos=$(awk '$4=="0A" && $2 ~ /:%04X$/ {print $10}' /proc/net/tcp /proc/net/tcp6); `+
			`for ino in $inos; do for p in /proc/[0-9]*; do `+
			`sudo ls -l "$p/fd" 2>/dev/null | grep -q "socket:\[$ino\]" && sudo kill "${p#/proc/}"; `+
			`done; done; true`, port)
}

// excludeServer returns servers without the entry matching serverID,
// preserving order.
func excludeServer(servers []RegisteredServer, serverID string) []RegisteredServer {
	out := make([]RegisteredServer, 0, len(servers))
	for _, s := range servers {
		if s.ServerID != serverID {
			out = append(out, s)
		}
	}
	return out
}

// UnenrollServer removes an enrolled server from the relay completely:
// config (authorized_keys line, xray inbound/rules, Caddyfile handle, CA
// cert, registry entry) AND live state (established VLESS sessions, the
// reverse-tunnel sshd session). Order: block re-auth first, then drop live
// state, then clean files, then forget locally — a mid-way failure leaves
// the registry entry intact, and every step is idempotent, so re-running
// the command retries cleanly.
func (o *Ops) UnenrollServer(serverID string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}
	const total = 4

	// Step 1: Resolve the target in the registry.
	progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "running"})
	all, err := o.ListServers()
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "failed", Error: err.Error()})
		return fmt.Errorf("listing registered servers: %w", err)
	}
	var target *RegisteredServer
	for i := range all {
		if all[i].ServerID == serverID {
			target = &all[i]
		}
	}
	if target == nil {
		err := fmt.Errorf("server %q is not enrolled", serverID)
		progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "failed", Error: err.Error()})
		return err
	}
	remaining := excludeServer(all, serverID)
	progress(ProgressEvent{Step: 1, Total: total, Label: "Resolve server", Status: "completed",
		Message: fmt.Sprintf("%s on port %d", target.ServerID, target.RemotePort)})

	// Step 2: Render everything from the REMAINING tenant list (full-rewrite
	// philosophy, same as enroll) — outside the SSH callback, fail fast.
	progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "running"})
	cfg := o.Config()
	servers, tenants, _, err := o.relayTenantState(remaining)
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return err
	}
	caddyfile, err := caddy.RenderCaddyfile(caddy.Config{Domain: cfg.Xray.RelayHost, Servers: servers})
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return fmt.Errorf("rendering Caddyfile: %w", err)
	}
	xjson, err := relayxray.RenderConfig(relayxray.Config{Tenants: tenants})
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return fmt.Errorf("rendering relay Xray config: %w", err)
	}
	adminPubKey, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "failed", Error: err.Error()})
		return fmt.Errorf("reading admin public key: %w", err)
	}
	akContent := renderRelayAuthorizedKeys(string(adminPubKey), remaining)
	sshUser := cfg.Server.RelaySSHUser
	progress(ProgressEvent{Step: 2, Total: total, Label: "Render remaining config", Status: "completed",
		Message: fmt.Sprintf("%d tenants remain (admin + %d servers)", len(tenants), len(remaining))})

	// Step 3: Apply on the relay — ONE SSH connection for everything, so no
	// step after the Caddy reload needs a fresh TLS handshake.
	progress(ProgressEvent{Step: 3, Total: total, Label: "Remove from relay", Status: "running"})
	err = o.RelaySSH(func(client *gossh.Client) error {
		// (a) Block re-auth: rewrite authorized_keys without the target.
		akPath := fmt.Sprintf("/home/%s/.ssh/authorized_keys", sshUser)
		akB64 := base64.StdEncoding.EncodeToString([]byte(akContent))
		writeKeys := fmt.Sprintf(
			"echo %s | base64 -d | sudo tee %s >/dev/null && sudo chown %s:%s %s && sudo chmod 600 %s",
			akB64, akPath, sshUser, sshUser, akPath, akPath,
		)
		if err := runRelayCmd(client, writeKeys); err != nil {
			return fmt.Errorf("writing authorized_keys: %w", err)
		}

		// (b) Hot-remove the running inbound + rules; severs the tenant's
		// established VLESS sessions (server transport and all its clients).
		if err := liveRemoveTenant(client, serverID); err != nil {
			return err
		}

		// (c) Kill the reverse-tunnel sshd session still holding the
		// listener on the tenant's port.
		if err := runRelayCmd(client, killRelayListenerCmd(target.RemotePort)); err != nil {
			return fmt.Errorf("killing reverse-tunnel session: %w", err)
		}

		// (d) Rewrite the Caddyfile, validate, graceful reload; drop the CA.
		cfB64 := base64.StdEncoding.EncodeToString([]byte(caddyfile))
		if err := runRelayCmd(client, fmt.Sprintf("echo %s | base64 -d | sudo tee /etc/caddy/Caddyfile >/dev/null", cfB64)); err != nil {
			return fmt.Errorf("writing Caddyfile: %w", err)
		}
		if err := runRelayCmd(client, "sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile"); err != nil {
			return fmt.Errorf("relay Caddyfile failed validation (not reloaded): %w", err)
		}
		if err := runRelayCmd(client, "sudo systemctl reload caddy"); err != nil {
			return fmt.Errorf("reloading caddy: %w", err)
		}
		if err := runRelayCmd(client, fmt.Sprintf("sudo rm -f /etc/caddy/ca/%s.crt", serverID)); err != nil {
			return fmt.Errorf("removing CA cert: %w", err)
		}

		// (e) Persist the xray config for restarts/reboots (the running
		// process was already updated in (b) — no restart).
		xB64 := base64.StdEncoding.EncodeToString([]byte(xjson))
		if err := runRelayCmd(client, fmt.Sprintf("echo %s | base64 -d | sudo tee /usr/local/etc/xray/config.json >/dev/null", xB64)); err != nil {
			return fmt.Errorf("writing relay Xray config: %w", err)
		}
		return nil
	})
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: total, Label: "Remove from relay", Status: "failed", Error: err.Error()})
		return fmt.Errorf("removing from relay: %w", err)
	}
	progress(ProgressEvent{Step: 3, Total: total, Label: "Remove from relay", Status: "completed",
		Message: "config removed, live connections killed"})

	// Step 4: Forget locally — LAST, so any earlier failure keeps the entry
	// and the command can simply be re-run.
	progress(ProgressEvent{Step: 4, Total: total, Label: "Remove from registry", Status: "running"})
	if err := os.Remove(filepath.Join(RegistryDir(), serverID+".json")); err != nil {
		progress(ProgressEvent{Step: 4, Total: total, Label: "Remove from registry", Status: "failed", Error: err.Error()})
		return fmt.Errorf("removing registry entry: %w", err)
	}
	progress(ProgressEvent{Step: 4, Total: total, Label: "Remove from registry", Status: "completed",
		Message: fmt.Sprintf("%s un-enrolled", serverID)})
	return nil
}
