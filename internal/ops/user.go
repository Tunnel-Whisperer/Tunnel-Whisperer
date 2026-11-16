package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/pki"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
	twxray "github.com/tunnelwhisperer/tw/internal/xray"
	proxymanCmd "github.com/xtls/xray-core/app/proxyman/command"
	statsCmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	gossh "golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v3"
)

// UserInfo describes one user.
type UserInfo struct {
	Name          string          `json:"name"`
	UUID          string          `json:"uuid,omitempty"`
	Tunnels       []config.Tunnel `json:"tunnels,omitempty"`
	HasKey        bool            `json:"has_key"`
	Active        bool            `json:"active"`
	Online        bool            `json:"online"`
	SingleSession bool            `json:"single_session"`
	Sessions      int             `json:"sessions"`
	ExpiresAt     string          `json:"expires_at,omitempty"`
	Expired       bool            `json:"expired"`
	DirPath       string          `json:"-"`
}

// CreateUserRequest holds the parameters for creating a new user.
type CreateUserRequest struct {
	Name          string               `json:"name"`
	Mappings      []config.PortMapping `json:"mappings"`
	SingleSession bool                 `json:"single_session"`
	ExpiresAt     time.Time            `json:"expires_at,omitzero"`
}

// ListApplications returns all configured application templates.
func (o *Ops) ListApplications() []config.Application {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cfg.Server.Applications
}

// CreateApplication adds a new application template to the config.
func (o *Ops) CreateApplication(app config.Application) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(app.Name) {
		return fmt.Errorf("invalid application name: must contain only letters, numbers, dashes, and underscores")
	}
	if len(app.Mappings) == 0 {
		return fmt.Errorf("at least one port mapping is required")
	}
	for _, m := range app.Mappings {
		if m.ClientPort < 1 || m.ClientPort > 65535 || m.ServerPort < 1 || m.ServerPort > 65535 {
			return fmt.Errorf("port numbers must be between 1 and 65535")
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	for _, existing := range o.cfg.Server.Applications {
		if strings.EqualFold(existing.Name, app.Name) {
			return fmt.Errorf("application %q already exists", app.Name)
		}
	}

	o.cfg.Server.Applications = append(o.cfg.Server.Applications, app)
	return config.Save(o.cfg)
}

// UpdateApplication replaces an existing application template in the config.
func (o *Ops) UpdateApplication(name string, app config.Application) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(app.Name) {
		return fmt.Errorf("invalid application name: must contain only letters, numbers, dashes, and underscores")
	}
	if len(app.Mappings) == 0 {
		return fmt.Errorf("at least one port mapping is required")
	}
	for _, m := range app.Mappings {
		if m.ClientPort < 1 || m.ClientPort > 65535 || m.ServerPort < 1 || m.ServerPort > 65535 {
			return fmt.Errorf("port numbers must be between 1 and 65535")
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	// If renaming, check that the new name doesn't conflict.
	if !strings.EqualFold(name, app.Name) {
		for _, existing := range o.cfg.Server.Applications {
			if strings.EqualFold(existing.Name, app.Name) {
				return fmt.Errorf("application %q already exists", app.Name)
			}
		}
	}

	for i, a := range o.cfg.Server.Applications {
		if a.Name == name {
			o.cfg.Server.Applications[i] = app
			return config.Save(o.cfg)
		}
	}
	return fmt.Errorf("application %q not found", name)
}

// DeleteApplication removes an application template from the config.
func (o *Ops) DeleteApplication(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	apps := o.cfg.Server.Applications
	for i, a := range apps {
		if a.Name == name {
			o.cfg.Server.Applications = append(apps[:i], apps[i+1:]...)
			return config.Save(o.cfg)
		}
	}
	return fmt.Errorf("application %q not found", name)
}

// ListUsers returns all users found in the users directory.
func (o *Ops) ListUsers() ([]UserInfo, error) {
	usersDir := config.UsersDir()
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var users []UserInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ui := UserInfo{
			Name:    e.Name(),
			DirPath: filepath.Join(usersDir, e.Name()),
		}

		// Try to read the client config.
		cfgPath := filepath.Join(ui.DirPath, "config.yaml")
		if data, err := os.ReadFile(cfgPath); err == nil {
			var clientCfg struct {
				Xray   config.XrayConfig   `yaml:"xray"`
				Client config.ClientConfig `yaml:"client"`
			}
			if yaml.Unmarshal(data, &clientCfg) == nil {
				// Xray treats UUIDs case-insensitively but the relay accepts
				// only the canonical lowercase form; a hand-edited or older
				// config may carry uppercase.
				ui.UUID = strings.ToLower(clientCfg.Xray.UUID)
				ui.Tunnels = clientCfg.Client.Tunnels
			}
		}

		// Invited users leave only public material on the server; the private
		// half is born on (and never leaves) the client machine.
		if _, err := os.Stat(filepath.Join(ui.DirPath, "id_ed25519.pub")); err == nil {
			ui.HasKey = true
		}
		if _, err := os.Stat(filepath.Join(ui.DirPath, ".applied")); err == nil {
			ui.Active = true
		}
		if _, err := os.Stat(filepath.Join(ui.DirPath, ".single-session")); err == nil {
			ui.SingleSession = true
		}

		if exp, err := userExpiry(ui.DirPath); err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("user client.crt unreadable", "user", ui.Name, "error", err)
			}
		} else if !exp.IsZero() {
			ui.ExpiresAt = exp.UTC().Format(time.RFC3339)
			ui.Expired = time.Now().After(exp)
		}

		users = append(users, ui)
	}

	// Populate session counts from the SSH server's connection tracker.
	sessionCounts := o.GetSessionCounts()
	for i := range users {
		if count, ok := sessionCounts[users[i].Name]; ok {
			users[i].Sessions = count
		}
	}

	return users, nil
}

// userExpiry returns the NotAfter of the user's stored client.crt, or the zero
// time with an os.IsNotExist error when the user has none (pre-expiry users).
func userExpiry(dirPath string) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(dirPath, "client.crt"))
	if err != nil {
		return time.Time{}, err
	}
	return pki.CertNotAfter(data)
}

// validateUserName enforces the user-name shape ([A-Za-z0-9_-]+). Every
// function that joins a user name onto config.UsersDir() must call it so a
// name like ".." cannot escape the users directory.
func validateUserName(name string) error {
	if name == "" {
		return fmt.Errorf("user name is required")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("user name must contain only letters, numbers, dashes, and underscores")
		}
	}
	return nil
}

// validateCreateUser runs the guards for InviteUser: name shape, non-empty
// mappings, a configured relay/UUID, and that the user doesn't already exist.
func validateCreateUser(cfg config.Config, req CreateUserRequest) error {
	if err := validateUserName(req.Name); err != nil {
		return err
	}
	if len(req.Mappings) == 0 {
		return fmt.Errorf("at least one port mapping is required")
	}
	if cfg.Xray.RelayHost == "" {
		return fmt.Errorf("xray.relay_host must be configured before creating users")
	}
	if cfg.Xray.UUID == "" {
		return fmt.Errorf("server UUID must be set — run `tw server start` or `tw relay create` first")
	}

	userDir := filepath.Join(config.UsersDir(), req.Name)
	if _, err := os.Stat(userDir); err == nil {
		if exp, err := userExpiry(userDir); err == nil && !exp.After(time.Now()) {
			return fmt.Errorf("user %q has expired; delete it first (tw server user delete %s)", req.Name, req.Name)
		}
		return fmt.Errorf("user %q already exists", req.Name)
	}
	return nil
}

// DeleteUser removes a user's UUID from the relay, then removes the user
// directory and their authorized_keys entry.
func (o *Ops) DeleteUser(name string) error {
	if err := validateUserName(name); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	userDir := filepath.Join(config.UsersDir(), name)
	if _, err := os.Stat(userDir); os.IsNotExist(err) {
		return fmt.Errorf("user %q not found", name)
	}

	// Read the user's UUID so we can remove it from the relay.
	cfgPath := filepath.Join(userDir, "config.yaml")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var clientCfg struct {
			Xray config.XrayConfig `yaml:"xray"`
		}
		if yaml.Unmarshal(data, &clientCfg) == nil && clientCfg.Xray.UUID != "" {
			if err := removeUUIDFromRelay(o.cfg, strings.ToLower(clientCfg.Xray.UUID)); err != nil {
				slog.Warn("could not remove UUID from relay", "user", name, "error", err)
			}
		}
	}

	// Revoke SSH access BEFORE the user dir (and with it the public key) is
	// gone, so a failure leaves a user that can be deleted again rather than
	// a "deleted" user whose key still authenticates.
	pubData, err := os.ReadFile(filepath.Join(userDir, "id_ed25519.pub"))
	switch {
	case err == nil:
		err = removeAuthorizedKey(pubData)
	case os.IsNotExist(err):
		err = removeAuthorizedKeysByComment(name)
	}
	if err != nil {
		return fmt.Errorf("removing %q from authorized_keys: %w", name, err)
	}

	if err := os.RemoveAll(userDir); err != nil {
		return fmt.Errorf("removing user directory: %w", err)
	}

	// Removing the key only blocks new handshakes; end the live sessions too.
	o.srv.DisconnectUser(name)
	o.srv.forgetExpiry(name)

	return nil
}

// SetUserSingleSession enables or disables the single-session flag for a user.
// When enabled, the SSH server rejects a second connection with the same key.
func (o *Ops) SetUserSingleSession(name string, enabled bool) error {
	if err := validateUserName(name); err != nil {
		return err
	}
	userDir := filepath.Join(config.UsersDir(), name)
	if _, err := os.Stat(userDir); err != nil {
		return fmt.Errorf("user %q not found", name)
	}

	markerPath := filepath.Join(userDir, ".single-session")
	if enabled {
		if err := os.WriteFile(markerPath, nil, 0644); err != nil {
			return fmt.Errorf("writing single-session marker: %w", err)
		}
	} else {
		_ = os.Remove(markerPath)
	}

	// Rebuild authorized_keys entry with updated options.
	pubPath := filepath.Join(userDir, "id_ed25519.pub")
	pubData, err := os.ReadFile(pubPath)
	if err != nil {
		return nil // no key — nothing to update
	}

	// Read user config to get current port mappings.
	cfgPath := filepath.Join(userDir, "config.yaml")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}
	var clientCfg struct {
		Client config.ClientConfig `yaml:"client"`
	}
	if err := yaml.Unmarshal(cfgData, &clientCfg); err != nil {
		return nil
	}

	var serverPorts []int
	for _, t := range clientCfg.Client.Tunnels {
		serverPorts = append(serverPorts, t.RemotePort)
	}

	line, err := authorizedKeyLine(pubData, name, serverPorts, enabled)
	if err != nil {
		return err
	}
	match := authorizedKeyMatch(pubData)
	err = updateAuthorizedKeys(func(lines []string) []string {
		kept := slices.DeleteFunc(lines, func(l string) bool { return strings.Contains(l, match) })
		return append(kept, line)
	})
	if err != nil {
		return fmt.Errorf("updating authorized_keys: %w", err)
	}

	return nil
}

// UnregisterUsers removes users from the current relay without deleting
// them. Their UUIDs are removed from the relay's Xray config and the
// .applied marker is cleared, but their local config and keys remain.
func (o *Ops) UnregisterUsers(ctx context.Context, names []string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return fmt.Errorf("no relay configured")
	}

	allUsers, err := o.ListUsers()
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	var targets []UserInfo
	if len(names) == 0 {
		targets = allUsers
	} else {
		nameSet := make(map[string]bool, len(names))
		for _, n := range names {
			nameSet[n] = true
		}
		for _, u := range allUsers {
			if nameSet[u.Name] {
				targets = append(targets, u)
			}
		}
	}

	if len(targets) == 0 {
		return fmt.Errorf("no users to unregister")
	}

	total := len(targets) + 1

	// Step 1: Remove UUIDs from relay config file.
	progress(ProgressEvent{Step: 1, Total: total, Label: "Removing from relay config", Status: "running"})
	if err := removeMultipleUUIDsFromRelayConfig(cfg, targets); err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Removing from relay config", Status: "failed", Error: err.Error()})
		return fmt.Errorf("updating relay: %w", err)
	}
	progress(ProgressEvent{Step: 1, Total: total, Label: "Removing from relay config", Status: "completed",
		Message: fmt.Sprintf("Removed %d UUIDs", len(targets))})

	// Remaining steps: Remove .applied marker from each user.
	for i, u := range targets {
		step := 2 + i
		progress(ProgressEvent{Step: step, Total: total, Label: u.Name, Status: "running"})
		os.Remove(filepath.Join(u.DirPath, ".applied"))
		progress(ProgressEvent{Step: step, Total: total, Label: u.Name, Status: "completed", Message: "unregistered"})
	}

	return nil
}

// removeMultipleUUIDsFromRelayConfig removes user UUIDs from the relay's
// Xray config. On the relay admin's own profile it edits the file on disk
// only (the running Xray process is not touched); a server goes through the
// relay's tenant request handler, which also drops them from the running
// Xray.
func removeMultipleUUIDsFromRelayConfig(cfg *config.Config, users []UserInfo) error {
	if len(users) == 0 {
		return nil
	}
	if !isRelayAdminProfile(cfg) {
		var uuids []string
		for _, u := range users {
			if u.UUID != "" {
				uuids = append(uuids, u.UUID)
			}
		}
		if len(uuids) == 0 {
			return nil
		}
		return relayTenantApply(cfg, "remove", uuids)
	}
	serverID := resolveServerID(cfg, config.ClientCertPath())
	inboundTag := "vless-in-" + serverID
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		release, err := lockRelayXrayConfig(client)
		if err != nil {
			return err
		}
		defer release()

		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf, inboundTag)
		if err != nil {
			return err
		}

		removeSet := make(map[string]bool, len(users))
		for _, u := range users {
			if u.UUID != "" {
				removeSet[u.UUID] = true
			}
		}

		filtered := make([]interface{}, 0, len(clients))
		for _, c := range clients {
			if cm, ok := c.(map[string]interface{}); ok {
				if id, _ := cm["id"].(string); removeSet[id] {
					continue
				}
			}
			filtered = append(filtered, c)
		}

		if len(filtered) == len(clients) {
			return nil // nothing to remove
		}

		settings["clients"] = filtered
		return writeRelayXrayConfig(client, xrayConf)
	})
}

// ApplyUsers registers users on the current relay. If names is empty,
// all users are applied. This treats the relay as brand-new: each user's
// UUID is added to the relay's Xray config, and the user's local config
// is updated with the current relay settings (domain, port, path) so
// downloaded config bundles always reflect the active relay.
func (o *Ops) ApplyUsers(ctx context.Context, names []string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return fmt.Errorf("no relay configured")
	}

	// Collect users to apply.
	allUsers, err := o.ListUsers()
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	var targets []UserInfo
	if len(names) == 0 {
		targets = allUsers
	} else {
		nameSet := make(map[string]bool, len(names))
		for _, n := range names {
			nameSet[n] = true
		}
		for _, u := range allUsers {
			if nameSet[u.Name] {
				targets = append(targets, u)
			}
		}
	}

	if len(targets) == 0 {
		return fmt.Errorf("no users to apply")
	}

	total := len(targets) + 1 // +1 for the relay connection step

	// Step 1: Register all UUIDs on the relay.
	progress(ProgressEvent{Step: 1, Total: total, Label: "Registering on relay", Status: "running"})
	uuids := make([]string, 0, len(targets))
	for _, u := range targets {
		if u.UUID != "" {
			uuids = append(uuids, u.UUID)
		}
	}
	if err := addMultipleUUIDsToRelay(cfg, uuids); err != nil {
		progress(ProgressEvent{Step: 1, Total: total, Label: "Registering on relay", Status: "failed", Error: err.Error()})
		return fmt.Errorf("updating relay: %w", err)
	}
	progress(ProgressEvent{Step: 1, Total: total, Label: "Registering on relay", Status: "completed",
		Message: fmt.Sprintf("Registered %d UUIDs", len(uuids))})

	// Step 2+: Update each user's config with current relay settings and mark applied.
	for i, u := range targets {
		step := i + 2
		progress(ProgressEvent{Step: step, Total: total, Label: u.Name, Status: "running"})

		if err := syncUserConfig(u.DirPath, cfg); err != nil {
			slog.Warn("could not update user config", "user", u.Name, "error", err)
			progress(ProgressEvent{Step: step, Total: total, Label: u.Name, Status: "completed",
				Message: "registered (config update failed: " + err.Error() + ")"})
		} else {
			progress(ProgressEvent{Step: step, Total: total, Label: u.Name, Status: "completed",
				Message: "registered and config updated"})
		}

		_ = os.WriteFile(filepath.Join(u.DirPath, ".applied"), nil, 0644)
	}

	return nil
}

// syncUserConfig updates a user's config.yaml with the current relay
// settings (domain, port, path, remote SSH port). This ensures downloaded
// config bundles always match the active relay, even after switching to a
// new relay with a different domain.
func syncUserConfig(userDir string, cfg *config.Config) error {
	cfgPath := filepath.Join(userDir, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}

	var clientCfg struct {
		Xray   config.XrayConfig   `yaml:"xray"`
		Client config.ClientConfig `yaml:"client"`
	}
	if err := yaml.Unmarshal(data, &clientCfg); err != nil {
		return err
	}

	clientCfg.Xray.RelayHost = cfg.Xray.RelayHost
	clientCfg.Xray.RelayPort = cfg.Xray.RelayPort
	clientCfg.Xray.Path = cfg.Xray.Path
	clientCfg.Client.ServerSSHPort = cfg.Server.RemotePort
	if hostKey, err := twssh.EnsureHostPublicKey(config.HostKeyDir()); err == nil {
		clientCfg.Client.ServerHostKey = hostKey
	} else {
		slog.Warn("could not read server SSH host key while syncing user config", "error", err)
	}

	updated, err := yaml.Marshal(clientCfg)
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, updated, 0644)
}

// deactivateAllUsers removes .applied markers from all user directories.
func deactivateAllUsers() {
	usersDir := config.UsersDir()
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		os.Remove(filepath.Join(usersDir, e.Name(), ".applied"))
	}
}

// addMultipleUUIDsToRelay opens a single SSH connection to the relay and
// adds all given UUIDs in one batch — much faster than calling addUUIDToRelay
// per-user.
func addMultipleUUIDsToRelay(cfg *config.Config, uuids []string) error {
	if len(uuids) == 0 {
		return nil
	}
	for _, u := range uuids {
		if !isCanonicalUUID(u) {
			return fmt.Errorf("refusing to register non-canonical UUID %q on relay", u)
		}
	}
	if !isRelayAdminProfile(cfg) {
		return relayTenantApply(cfg, "add", uuids)
	}
	serverID := resolveServerID(cfg, config.ClientCertPath())
	inboundTag := "vless-in-" + serverID
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		release, err := lockRelayXrayConfig(client)
		if err != nil {
			return err
		}
		defer release()

		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf, inboundTag)
		if err != nil {
			return err
		}

		// Build set of existing UUIDs.
		existing := make(map[string]bool, len(clients))
		for _, c := range clients {
			if cm, ok := c.(map[string]interface{}); ok {
				if id, _ := cm["id"].(string); id != "" {
					existing[id] = true
				}
			}
		}

		// Add missing UUIDs.
		var added []string
		for _, u := range uuids {
			if !existing[u] {
				clients = append(clients, map[string]interface{}{"id": u, "email": u})
				added = append(added, u)
			}
		}

		if len(added) > 0 {
			settings["clients"] = clients
			if err := writeRelayXrayConfig(client, xrayConf); err != nil {
				return err
			}
		}

		// Hot-add to running Xray via API; restart as fallback.
		// We send all requested UUIDs (not just newly added) in case
		// the running process is stale.
		if err := xrayAPIAddUsers(client, uuids, inboundTag); err != nil {
			slog.Warn("xray API add failed, restarting xray", "error", err)
			restartRelayXray(client)
		}
		return nil
	})
}

// authorizedKeyLine renders the authorized_keys line for a user key with
// permitopen restrictions and optional single-session enforcement.
func authorizedKeyLine(pubKey []byte, comment string, ports []int, singleSession bool) (string, error) {
	var options []string
	if singleSession {
		options = append(options, "single-session")
	}
	for _, port := range ports {
		options = append(options, fmt.Sprintf(`permitopen="127.0.0.1:%d"`, port))
	}

	// Canonicalize the caller-supplied key to a single, option-free line. This
	// is the sink chokepoint for BOTH the invite path (grantClient) and the
	// CreateUser/refresh path: without it, a newline embedded in the key would
	// inject a second, permitopen-free authorized_keys line and escape the
	// per-user port restrictions.
	keyLine, err := canonicalAuthorizedKey(string(pubKey))
	if err != nil {
		return "", fmt.Errorf("refusing to write invalid ssh public key to authorized_keys: %w", err)
	}
	return fmt.Sprintf("%s %s %s@tw", strings.Join(options, ","), keyLine, comment), nil
}

// appendAuthorizedKey adds a public key to the server's authorized_keys
// with permitopen restrictions and optional single-session enforcement.
func appendAuthorizedKey(pubKey []byte, comment string, ports []int, singleSession bool) error {
	line, err := authorizedKeyLine(pubKey, comment, ports, singleSession)
	if err != nil {
		return err
	}
	return updateAuthorizedKeys(func(lines []string) []string { return append(lines, line) })
}

// authorizedKeyMatch returns the substring identifying a key's lines: the
// base64 key data (lines carry options in front of it).
func authorizedKeyMatch(pubKey []byte) string {
	keyStr := strings.TrimSpace(string(pubKey))
	if parts := strings.Fields(keyStr); len(parts) >= 2 {
		return parts[1]
	}
	return keyStr
}

// removeAuthorizedKey removes lines containing the given public key.
func removeAuthorizedKey(pubKey []byte) error {
	match := authorizedKeyMatch(pubKey)
	return updateAuthorizedKeys(func(lines []string) []string {
		return slices.DeleteFunc(lines, func(l string) bool { return strings.Contains(l, match) })
	})
}

// removeAuthorizedKeysByComment removes the lines authorizedKeyLine wrote for
// user name (trailing "<name>@tw" comment), for when the key itself is gone.
func removeAuthorizedKeysByComment(name string) error {
	comment := name + "@tw"
	return updateAuthorizedKeys(func(lines []string) []string {
		return slices.DeleteFunc(lines, func(l string) bool {
			f := strings.Fields(l)
			return len(f) > 0 && f[len(f)-1] == comment
		})
	})
}

// updateAuthorizedKeys applies edit to the non-empty lines of authorized_keys
// (missing file = no lines) and atomically replaces the file with the result:
// the SSH server re-reads it on every channel, so it must never observe a
// truncated or half-written file.
func updateAuthorizedKeys(edit func(lines []string) []string) error {
	akPath := config.AuthorizedKeysPath()
	data, err := os.ReadFile(akPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading authorized_keys: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	lines = edit(lines)
	result := strings.Join(lines, "\n")
	if len(lines) > 0 {
		result += "\n"
	}
	return writeFileAtomic(akPath, []byte(result), 0o600)
}

// writeFileAtomic writes data to a temp file in path's directory, fsyncs it,
// and renames it over path, so readers see either the old or the new file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// withRelaySSH opens a temporary Xray tunnel to the relay, establishes an
// SSH connection, and passes it to fn. The tunnel and connection are torn
// down automatically when fn returns.
// loopbackPortFree reports whether 127.0.0.1:port can currently be bound.
func loopbackPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// freeLoopbackPort asks the OS for an available loopback port.
func freeLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func withRelaySSH(cfg *config.Config, fn func(client *gossh.Client) error) error {
	// Present the server's client cert so the relay's mTLS gate admits this
	// management tunnel (covers TestRelay, the SSH terminal, enrollment, and
	// user add/remove — every withRelaySSH caller).
	applyClientCertPaths(&cfg.Xray)
	xrayInstance, err := twxray.New(cfg.Xray)
	if err != nil {
		return fmt.Errorf("initializing Xray: %w", err)
	}
	// The management tunnel needs a free local loopback port for its Xray
	// inbound. Reusing one fixed port (TempXrayPort+1) breaks back-to-back or
	// concurrent tunnels: the previous tunnel's listener is not always released
	// the instant Close() returns (notably on Windows: "Only one usage of each
	// socket address"). Prefer the configured port, but fall back to an
	// OS-assigned free port so tunnels never collide.
	tempPort := cfg.Server.TempXrayPort
	if tempPort == 0 {
		tempPort = 59000
	}
	listenPort := tempPort + 1
	if !loopbackPortFree(listenPort) {
		p, perr := freeLoopbackPort()
		if perr != nil {
			return fmt.Errorf("allocating local tunnel port: %w", perr)
		}
		listenPort = p
	}
	if err := xrayInstance.Start(listenPort, cfg.Server.RelaySSHPort, cfg.Proxy); err != nil {
		// The port may have been taken between the free-check and the bind
		// (tight race / lingering listener). Retry once on a fresh free port.
		p, perr := freeLoopbackPort()
		if perr != nil {
			return fmt.Errorf("starting Xray: %w", err)
		}
		listenPort = p
		if err := xrayInstance.Start(listenPort, cfg.Server.RelaySSHPort, cfg.Proxy); err != nil {
			return fmt.Errorf("starting Xray: %w", err)
		}
	}
	defer xrayInstance.Close()

	privPath := filepath.Join(config.Dir(), "id_ed25519")
	keyData, err := os.ReadFile(privPath)
	if err != nil {
		return fmt.Errorf("reading server key: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing server key: %w", err)
	}

	xrayAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)

	var client *gossh.Client
	for i := 0; i < 15; i++ {
		client, err = dialRelaySSH(xrayAddr, cfg, signer, gossh.InsecureIgnoreHostKey(), 15*time.Second)
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("SSH to relay: %w", err)
	}
	defer client.Close()

	return fn(client)
}

// readRelayXrayConfig reads and parses the Xray config from the relay.
func readRelayXrayConfig(client *gossh.Client) (map[string]interface{}, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	out, err := session.Output("sudo cat /usr/local/etc/xray/config.json")
	session.Close()
	if err != nil {
		return nil, fmt.Errorf("reading relay config: %w", err)
	}

	var xrayConf map[string]interface{}
	if err := json.Unmarshal(out, &xrayConf); err != nil {
		return nil, fmt.Errorf("parsing relay config: %w", err)
	}
	return xrayConf, nil
}

// writeRelayXrayConfig writes the Xray config to the relay for persistence.
// It does NOT reload Xray — callers should use the API or restart separately.
func writeRelayXrayConfig(client *gossh.Client, xrayConf map[string]interface{}) error {
	updatedJSON, err := json.MarshalIndent(xrayConf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	session.Stdin = bytes.NewReader(updatedJSON)
	err = session.Run("sudo tee /usr/local/etc/xray/config.json > /dev/null")
	session.Close()
	if err != nil {
		return fmt.Errorf("writing relay config: %w", err)
	}

	return nil
}

// dialRelayGRPC creates a gRPC connection to the relay's Xray API
// (127.0.0.1:10085) tunneled through the SSH connection.
func dialRelayGRPC(client *gossh.Client) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(
		"passthrough:///relay-xray-api",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return client.Dial("tcp", "127.0.0.1:10085")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial relay API: %w", err)
	}
	return conn, nil
}

// xrayAPIAddUsers hot-adds UUIDs to the running Xray process via gRPC.
// Each UUID is added as a VLESS client on the per-tenant inbound identified
// by inboundTag (e.g. "vless-in-<server-id>").
func xrayAPIAddUsers(client *gossh.Client, uuids []string, inboundTag string) error {
	if len(uuids) == 0 {
		return nil
	}

	conn, err := dialRelayGRPC(client)
	if err != nil {
		return err
	}
	defer conn.Close()

	hsClient := proxymanCmd.NewHandlerServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, u := range uuids {
		_, err := hsClient.AlterInbound(ctx, &proxymanCmd.AlterInboundRequest{
			Tag: inboundTag,
			Operation: serial.ToTypedMessage(&proxymanCmd.AddUserOperation{
				User: &protocol.User{
					Email: u,
					Account: serial.ToTypedMessage(&vless.Account{
						Id: u,
					}),
				},
			}),
		})
		if err != nil {
			return fmt.Errorf("add user %s: %w", u[:8], err)
		}
		slog.Info("xray API: user added", "uuid", u[:8])
	}
	return nil
}

// xrayAPIRemoveUsers removes UUIDs from the running Xray process via gRPC.
// inboundTag identifies the per-tenant VLESS inbound (e.g. "vless-in-<server-id>").
func xrayAPIRemoveUsers(client *gossh.Client, uuids []string, inboundTag string) error {
	if len(uuids) == 0 {
		return nil
	}

	conn, err := dialRelayGRPC(client)
	if err != nil {
		return err
	}
	defer conn.Close()

	hsClient := proxymanCmd.NewHandlerServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, u := range uuids {
		_, err := hsClient.AlterInbound(ctx, &proxymanCmd.AlterInboundRequest{
			Tag: inboundTag,
			Operation: serial.ToTypedMessage(&proxymanCmd.RemoveUserOperation{
				Email: u,
			}),
		})
		if err != nil {
			return fmt.Errorf("remove user %s: %w", u[:8], err)
		}
		slog.Info("xray API: user removed", "uuid", u[:8])
	}
	return nil
}

// restartRelayXray restarts Xray on the relay as a last resort. The restart
// kills our VLESS tunnel (and thus the SSH session), but systemd completes
// the restart independently. The error from session.Run is expected.
func restartRelayXray(client *gossh.Client) {
	session, err := client.NewSession()
	if err != nil {
		return
	}
	defer session.Close()
	_ = session.Run("sudo systemctl restart xray")
}

// relayClients extracts the clients slice from the VLESS inbound in the
// parsed Xray config.  It finds the inbound by exact tag (inboundTag, e.g.
// "vless-in-<server-id>") first; falls back to protocol=="vless" for
// single-tenant relays where the tag is unambiguous.
func relayClients(xrayConf map[string]interface{}, inboundTag string) (settings map[string]interface{}, clients []interface{}, err error) {
	inbounds, _ := xrayConf["inbounds"].([]interface{})
	if len(inbounds) == 0 {
		return nil, nil, fmt.Errorf("no inbounds in relay config")
	}

	var inbound map[string]interface{}
	for _, ib := range inbounds {
		m, ok := ib.(map[string]interface{})
		if !ok {
			continue
		}
		if tag, _ := m["tag"].(string); tag == inboundTag {
			inbound = m
			break
		}
	}
	if inbound == nil {
		// fallback: pick the first VLESS inbound (harmless on single-tenant relays)
		for _, ib := range inbounds {
			m, ok := ib.(map[string]interface{})
			if !ok {
				continue
			}
			if proto, _ := m["protocol"].(string); proto == "vless" {
				inbound = m
				break
			}
		}
	}
	if inbound == nil {
		return nil, nil, fmt.Errorf("no VLESS inbound in relay config")
	}

	settings, _ = inbound["settings"].(map[string]interface{})
	clients, _ = settings["clients"].([]interface{})
	return settings, clients, nil
}

// addUUIDToRelay connects to the relay via a temporary Xray tunnel and
// adds a new client UUID to this profile's inbound in the relay's Xray
// config.  Persists to disk first, then hot-adds via the Xray API.  Falls
// back to restart if the API fails. A server asks the relay's tenant request
// handler to do this; only the relay admin's profile edits the file itself.
func addUUIDToRelay(cfg *config.Config, newUUID string) error {
	if !isCanonicalUUID(newUUID) {
		return fmt.Errorf("refusing to register non-canonical UUID %q on relay", newUUID)
	}
	if !isRelayAdminProfile(cfg) {
		return relayTenantApply(cfg, "add", []string{newUUID})
	}
	serverID := resolveServerID(cfg, config.ClientCertPath())
	inboundTag := "vless-in-" + serverID
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		release, err := lockRelayXrayConfig(client)
		if err != nil {
			return err
		}
		defer release()

		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf, inboundTag)
		if err != nil {
			return err
		}

		alreadyPresent := false
		for _, c := range clients {
			if cm, ok := c.(map[string]interface{}); ok {
				if id, _ := cm["id"].(string); id == newUUID {
					alreadyPresent = true
					break
				}
			}
		}

		if !alreadyPresent {
			clients = append(clients, map[string]interface{}{"id": newUUID, "email": newUUID})
			settings["clients"] = clients

			if err := writeRelayXrayConfig(client, xrayConf); err != nil {
				return err
			}
		}

		// Hot-add to running Xray via API; restart as fallback.
		if err := xrayAPIAddUsers(client, []string{newUUID}, inboundTag); err != nil {
			slog.Warn("xray API add failed, restarting xray", "error", err)
			restartRelayXray(client)
		}
		return nil
	})
}

// removeUUIDFromRelay connects to the relay via a temporary Xray tunnel
// and removes a client UUID from the relay's Xray config.  Persists to
// disk first, then hot-removes via the Xray API.  Falls back to restart.
// A server asks the relay's tenant request handler to do this.
func removeUUIDFromRelay(cfg *config.Config, targetUUID string) error {
	if !isRelayAdminProfile(cfg) {
		return relayTenantApply(cfg, "remove", []string{targetUUID})
	}
	serverID := resolveServerID(cfg, config.ClientCertPath())
	inboundTag := "vless-in-" + serverID
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		release, err := lockRelayXrayConfig(client)
		if err != nil {
			return err
		}
		defer release()

		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf, inboundTag)
		if err != nil {
			return err
		}

		filtered := make([]interface{}, 0, len(clients))
		for _, c := range clients {
			if cm, ok := c.(map[string]interface{}); ok {
				if id, _ := cm["id"].(string); id == targetUUID {
					continue // skip — this is the one to remove
				}
			}
			filtered = append(filtered, c)
		}

		if len(filtered) == len(clients) {
			return nil // UUID not found, nothing to do
		}

		settings["clients"] = filtered

		if err := writeRelayXrayConfig(client, xrayConf); err != nil {
			return err
		}

		// Hot-remove from running Xray via API; restart as fallback.
		if err := xrayAPIRemoveUsers(client, []string{targetUUID}, inboundTag); err != nil {
			slog.Warn("xray API remove failed, restarting xray", "error", err)
			restartRelayXray(client)
		}
		return nil
	})
}

// sshThroughServerTunnel opens an SSH connection to the relay using the
// server's already-running Xray tunnel (dokodemo-door on serverXrayPort).
// This is much faster than withRelaySSH since it doesn't create a
// temporary Xray instance.
func (o *Ops) sshThroughServerTunnel(cfg *config.Config, fn func(*gossh.Client) error) error {
	xrayAddr := fmt.Sprintf("127.0.0.1:%d", serverXrayPort(cfg))

	hostKeyCallback, hostKeyAlgos, err := relayTunnelHostKeyCallback()
	if err != nil {
		return err
	}

	privPath := filepath.Join(config.Dir(), "id_ed25519")
	keyData, err := os.ReadFile(privPath)
	if err != nil {
		return fmt.Errorf("reading server key: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing server key: %w", err)
	}

	sshCfg := &gossh.ClientConfig{
		User:              cfg.Server.RelaySSHUser,
		Auth:              []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: hostKeyAlgos,
		Timeout:           10 * time.Second,
	}

	client, err := gossh.Dial("tcp", xrayAddr, sshCfg)
	if err != nil {
		return fmt.Errorf("SSH to relay via server tunnel: %w", err)
	}
	defer client.Close()
	return fn(client)
}

// relayTunnelHostKeyCallback verifies the relay's sshd behind the server's Xray
// tunnel against the pinned relay host key when one is recorded. Without a pin
// (a relay provisioned before pinning) it keeps the previous behaviour: the
// mTLS-authenticated VLESS tunnel is the only relay authentication.
func relayTunnelHostKeyCallback() (gossh.HostKeyCallback, []string, error) {
	pinned, err := loadPinnedRelayHostKey()
	if err != nil {
		return nil, nil, fmt.Errorf("loading pinned relay host key: %w", err)
	}
	if pinned == nil {
		return gossh.InsecureIgnoreHostKey(), nil, nil
	}
	return gossh.FixedHostKey(pinned), []string{pinned.Type()}, nil
}

// InvalidateOnlineCache clears the online status cache so the next
// GetOnlineUsers call triggers a fresh query to the relay.
func (o *Ops) InvalidateOnlineCache() {
	o.onlineMu.Lock()
	o.onlinePoll = time.Time{}
	o.onlineMu.Unlock()
}

// GetOnlineUsers returns a cached map of UUID → online status.
// The cache is refreshed via the server's running Xray tunnel when stale.
// Returns nil if no relay is configured or the server tunnel isn't running.
func (o *Ops) GetOnlineUsers() map[string]bool {
	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return nil
	}

	// Online status is only meaningful when the server's Xray tunnel is up.
	if !o.srv.Status().Xray {
		return nil
	}

	// Return cache if fresh (< 20 seconds).
	o.onlineMu.RLock()
	if o.onlineCache != nil && time.Since(o.onlinePoll) < 20*time.Second {
		cache := make(map[string]bool, len(o.onlineCache))
		for k, v := range o.onlineCache {
			cache[k] = v
		}
		o.onlineMu.RUnlock()
		return cache
	}
	o.onlineMu.RUnlock()

	return o.refreshOnlineStatus(cfg)
}

// refreshOnlineStatus queries the relay's StatsService for online users
// via the server's existing Xray tunnel and updates the cache.
func (o *Ops) refreshOnlineStatus(cfg *config.Config) map[string]bool {
	// Prevent concurrent refreshes — return stale cache instead.
	if !o.onlineRefresh.TryLock() {
		o.onlineMu.RLock()
		defer o.onlineMu.RUnlock()
		if o.onlineCache == nil {
			return make(map[string]bool)
		}
		cache := make(map[string]bool, len(o.onlineCache))
		for k, v := range o.onlineCache {
			cache[k] = v
		}
		return cache
	}
	defer o.onlineRefresh.Unlock()

	result := make(map[string]bool)

	err := o.sshThroughServerTunnel(cfg, func(client *gossh.Client) error {
		conn, err := dialRelayGRPC(client)
		if err != nil {
			return err
		}
		defer conn.Close()

		sc := statsCmd.NewStatsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Try dedicated online stats first (Xray statsUserOnline).
		resp, err := sc.QueryStats(ctx, &statsCmd.QueryStatsRequest{Pattern: "online"})
		if err != nil {
			return fmt.Errorf("QueryStats: %w", err)
		}

		for _, s := range resp.GetStat() {
			parts := strings.Split(s.GetName(), ">>>")
			if len(parts) >= 3 && parts[0] == "user" && parts[len(parts)-1] == "online" && s.GetValue() > 0 {
				result[parts[1]] = true
			}
		}

		// Fallback: if no online stats (Xray version doesn't support
		// statsUserOnline), use traffic stats to detect recently active
		// users. Reset counters so each poll interval only detects users
		// with traffic since the last check.
		if len(resp.GetStat()) == 0 {
			trafficResp, tErr := sc.QueryStats(ctx, &statsCmd.QueryStatsRequest{
				Pattern: "user>>>",
				Reset_:  true,
			})
			if tErr == nil && o.trafficReset {
				serverUUID := cfg.Xray.UUID
				for _, s := range trafficResp.GetStat() {
					parts := strings.Split(s.GetName(), ">>>")
					// user>>>{email}>>>traffic>>>uplink/downlink
					if len(parts) == 4 && parts[0] == "user" && parts[2] == "traffic" && s.GetValue() > 0 {
						if parts[1] != serverUUID {
							result[parts[1]] = true
						}
					}
				}
			}
			o.trafficReset = true
		}

		slog.Debug("online status refreshed", "online_count", len(result))
		return nil
	})

	if err != nil {
		slog.Debug("online status refresh failed", "error", err)
	}

	o.onlineMu.Lock()
	o.onlineCache = result
	o.onlinePoll = time.Now()
	o.onlineMu.Unlock()

	return result
}
