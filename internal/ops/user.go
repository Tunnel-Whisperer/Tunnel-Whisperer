package ops

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tunnelwhisperer/tw/internal/config"
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
	Name    string          `json:"name"`
	UUID    string          `json:"uuid,omitempty"`
	Tunnels []config.Tunnel `json:"tunnels,omitempty"`
	HasKey  bool            `json:"has_key"`
	Active  bool            `json:"active"`
	Online  bool            `json:"online"`
	DirPath string          `json:"-"`
}

// PortMapping defines one client-port → server-port pair.
type PortMapping struct {
	ClientPort int `json:"client_port"`
	ServerPort int `json:"server_port"`
}

// CreateUserRequest holds the parameters for creating a new user.
type CreateUserRequest struct {
	Name     string        `json:"name"`
	Mappings []PortMapping `json:"mappings"`
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
				ui.UUID = clientCfg.Xray.UUID
				ui.Tunnels = clientCfg.Client.Tunnels
			}
		}

		if _, err := os.Stat(filepath.Join(ui.DirPath, "id_ed25519")); err == nil {
			ui.HasKey = true
		}
		if _, err := os.Stat(filepath.Join(ui.DirPath, ".applied")); err == nil {
			ui.Active = true
		}

		users = append(users, ui)
	}
	return users, nil
}

// CreateUser runs the user creation flow: generates credentials, updates the
// relay, saves config, and updates authorized_keys.
func (o *Ops) CreateUser(ctx context.Context, req CreateUserRequest, progress ProgressFunc) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	cfg := o.cfg

	// Validate.
	if req.Name == "" {
		return fmt.Errorf("user name is required")
	}
	for _, r := range req.Name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("user name must contain only letters, numbers, dashes, and underscores")
		}
	}
	if len(req.Mappings) == 0 {
		return fmt.Errorf("at least one port mapping is required")
	}
	if cfg.Xray.RelayHost == "" {
		return fmt.Errorf("xray.relay_host must be configured before creating users")
	}
	if cfg.Xray.UUID == "" {
		return fmt.Errorf("server UUID must be set — run `tw serve` or `tw create relay-server` first")
	}

	userDir := filepath.Join(config.UsersDir(), req.Name)
	if _, err := os.Stat(userDir); err == nil {
		return fmt.Errorf("user %q already exists", req.Name)
	}

	// Step 1: Generate credentials.
	progress(ProgressEvent{Step: 1, Total: 4, Label: "Generating credentials", Status: "running"})
	clientUUID := uuid.New().String()
	privPEM, pubAuthorized, err := twssh.GenerateKeyPair()
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: 4, Label: "Generating credentials", Status: "failed", Error: err.Error()})
		return fmt.Errorf("generating SSH key pair: %w", err)
	}
	progress(ProgressEvent{Step: 1, Total: 4, Label: "Generating credentials", Status: "completed", Message: "UUID: " + clientUUID})

	// Step 2: Update relay.
	progress(ProgressEvent{Step: 2, Total: 4, Label: "Updating relay", Status: "running"})
	if err := addUUIDToRelay(cfg, clientUUID); err != nil {
		slog.Warn("relay update failed", "error", err)
		progress(ProgressEvent{Step: 2, Total: 4, Label: "Updating relay", Status: "completed", Message: "Warning: " + err.Error()})
	} else {
		progress(ProgressEvent{Step: 2, Total: 4, Label: "Updating relay", Status: "completed", Message: "UUID added to relay"})
	}

	// Step 3: Save user files.
	progress(ProgressEvent{Step: 3, Total: 4, Label: "Saving configuration", Status: "running"})

	if err := os.MkdirAll(userDir, 0700); err != nil {
		progress(ProgressEvent{Step: 3, Total: 4, Label: "Saving configuration", Status: "failed", Error: err.Error()})
		return fmt.Errorf("creating user directory: %w", err)
	}

	if err := os.WriteFile(filepath.Join(userDir, "id_ed25519"), privPEM, 0600); err != nil {
		progress(ProgressEvent{Step: 3, Total: 4, Label: "Saving configuration", Status: "failed", Error: err.Error()})
		return fmt.Errorf("writing client private key: %w", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "id_ed25519.pub"), pubAuthorized, 0644); err != nil {
		progress(ProgressEvent{Step: 3, Total: 4, Label: "Saving configuration", Status: "failed", Error: err.Error()})
		return fmt.Errorf("writing client public key: %w", err)
	}

	tunnels := make([]config.Tunnel, len(req.Mappings))
	serverPorts := make([]int, len(req.Mappings))
	for i, m := range req.Mappings {
		tunnels[i] = config.Tunnel{
			LocalPort:  m.ClientPort,
			RemoteHost: "127.0.0.1",
			RemotePort: m.ServerPort,
		}
		serverPorts[i] = m.ServerPort
	}

	clientCfg := struct {
		Xray   config.XrayConfig   `yaml:"xray"`
		Client config.ClientConfig `yaml:"client"`
	}{
		Xray: config.XrayConfig{
			UUID:      clientUUID,
			RelayHost: cfg.Xray.RelayHost,
			RelayPort: cfg.Xray.RelayPort,
			Path:      cfg.Xray.Path,
		},
		Client: config.ClientConfig{
			SSHUser:       req.Name,
			ServerSSHPort: cfg.Server.RemotePort,
			Tunnels:       tunnels,
		},
	}

	cfgData, err := yaml.Marshal(clientCfg)
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: 4, Label: "Saving configuration", Status: "failed", Error: err.Error()})
		return fmt.Errorf("marshaling client config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "config.yaml"), cfgData, 0644); err != nil {
		progress(ProgressEvent{Step: 3, Total: 4, Label: "Saving configuration", Status: "failed", Error: err.Error()})
		return fmt.Errorf("writing client config: %w", err)
	}
	progress(ProgressEvent{Step: 3, Total: 4, Label: "Saving configuration", Status: "completed"})

	// Step 4: Update authorized_keys.
	progress(ProgressEvent{Step: 4, Total: 4, Label: "Updating authorized_keys", Status: "running"})
	if err := appendAuthorizedKey(pubAuthorized, req.Name, serverPorts); err != nil {
		progress(ProgressEvent{Step: 4, Total: 4, Label: "Updating authorized_keys", Status: "failed", Error: err.Error()})
		return fmt.Errorf("updating authorized_keys: %w", err)
	}
	progress(ProgressEvent{Step: 4, Total: 4, Label: "Updating authorized_keys", Status: "completed"})

	// Mark user as applied to the current relay.
	_ = os.WriteFile(filepath.Join(userDir, ".applied"), nil, 0644)

	return nil
}

// DeleteUser removes a user's UUID from the relay, then removes the user
// directory and their authorized_keys entry.
func (o *Ops) DeleteUser(name string) error {
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
			if err := removeUUIDFromRelay(o.cfg, clientCfg.Xray.UUID); err != nil {
				slog.Warn("could not remove UUID from relay", "user", name, "error", err)
			}
		}
	}

	// Read the user's public key so we can remove it from authorized_keys.
	pubPath := filepath.Join(userDir, "id_ed25519.pub")
	pubData, _ := os.ReadFile(pubPath)

	// Remove user directory.
	if err := os.RemoveAll(userDir); err != nil {
		return fmt.Errorf("removing user directory: %w", err)
	}

	// Remove from authorized_keys.
	if len(pubData) > 0 {
		if err := removeAuthorizedKey(pubData); err != nil {
			slog.Warn("could not remove authorized_keys entry", "user", name, "error", err)
		}
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
// Xray config file on disk. Does NOT touch the running Xray process.
func removeMultipleUUIDsFromRelayConfig(cfg *config.Config, users []UserInfo) error {
	if len(users) == 0 {
		return nil
	}
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf)
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
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf)
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
		if err := xrayAPIAddUsers(client, uuids); err != nil {
			slog.Warn("xray API add failed, restarting xray", "error", err)
			restartRelayXray(client)
		}
		return nil
	})
}

// GetUserConfigBundle returns the user's config files as a zip archive.
func (o *Ops) GetUserConfigBundle(name string) ([]byte, error) {
	userDir := filepath.Join(config.UsersDir(), name)
	if _, err := os.Stat(userDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("user %q not found", name)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	files := []string{"config.yaml", "id_ed25519", "id_ed25519.pub"}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(userDir, f))
		if err != nil {
			continue
		}
		w, err := zw.Create(f)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// appendAuthorizedKey adds a public key to the server's authorized_keys
// with permitopen restrictions.
func appendAuthorizedKey(pubKey []byte, comment string, ports []int) error {
	akPath := config.AuthorizedKeysPath()

	var options []string
	for _, port := range ports {
		options = append(options, fmt.Sprintf(`permitopen="127.0.0.1:%d"`, port))
	}

	keyLine := strings.TrimSpace(string(pubKey))
	line := fmt.Sprintf("%s %s %s@tw\n", strings.Join(options, ","), keyLine, comment)

	existing, err := os.ReadFile(akPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading authorized_keys: %w", err)
	}
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		existing = append(existing, '\n')
	}

	return os.WriteFile(akPath, append(existing, []byte(line)...), 0600)
}

// removeAuthorizedKey removes lines containing the given public key.
func removeAuthorizedKey(pubKey []byte) error {
	akPath := config.AuthorizedKeysPath()
	data, err := os.ReadFile(akPath)
	if err != nil {
		return err
	}

	keyStr := strings.TrimSpace(string(pubKey))
	// The key content (ssh-ed25519 AAAA...) may be wrapped with options;
	// match on the base64 portion.
	parts := strings.Fields(keyStr)
	var matchStr string
	if len(parts) >= 2 {
		matchStr = parts[1] // the base64 key data
	} else {
		matchStr = keyStr
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(line, matchStr) {
			continue // remove this line
		}
		kept = append(kept, line)
	}

	result := strings.Join(kept, "\n")
	if len(kept) > 0 {
		result += "\n"
	}
	return os.WriteFile(akPath, []byte(result), 0600)
}

// withRelaySSH opens a temporary Xray tunnel to the relay, establishes an
// SSH connection, and passes it to fn. The tunnel and connection are torn
// down automatically when fn returns.
func withRelaySSH(cfg *config.Config, fn func(client *gossh.Client) error) error {
	xrayInstance, err := twxray.New(cfg.Xray)
	if err != nil {
		return fmt.Errorf("initializing Xray: %w", err)
	}
	const tempPort = 59000
	if err := xrayInstance.Start(tempPort, cfg.Server.RelaySSHPort, cfg.Proxy); err != nil {
		return fmt.Errorf("starting Xray: %w", err)
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

	sshCfg := &gossh.ClientConfig{
		User:            cfg.Server.RelaySSHUser,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	xrayAddr := fmt.Sprintf("127.0.0.1:%d", tempPort+1)

	var client *gossh.Client
	for i := 0; i < 15; i++ {
		client, err = gossh.Dial("tcp", xrayAddr, sshCfg)
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
// Each UUID is added as a VLESS client on the "vless-in" inbound.
func xrayAPIAddUsers(client *gossh.Client, uuids []string) error {
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
			Tag: "vless-in",
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
func xrayAPIRemoveUsers(client *gossh.Client, uuids []string) error {
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
			Tag: "vless-in",
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
// parsed Xray config.  It finds the inbound by tag ("vless-in") or
// protocol ("vless") to avoid depending on array ordering.
func relayClients(xrayConf map[string]interface{}) (settings map[string]interface{}, clients []interface{}, err error) {
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
		if tag, _ := m["tag"].(string); tag == "vless-in" {
			inbound = m
			break
		}
		if proto, _ := m["protocol"].(string); proto == "vless" {
			inbound = m
			break
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
// adds a new client UUID to the relay's Xray config.  Persists to disk
// first, then hot-adds via the Xray API.  Falls back to restart if the
// API fails.
func addUUIDToRelay(cfg *config.Config, newUUID string) error {
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf)
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
		if err := xrayAPIAddUsers(client, []string{newUUID}); err != nil {
			slog.Warn("xray API add failed, restarting xray", "error", err)
			restartRelayXray(client)
		}
		return nil
	})
}

// removeUUIDFromRelay connects to the relay via a temporary Xray tunnel
// and removes a client UUID from the relay's Xray config.  Persists to
// disk first, then hot-removes via the Xray API.  Falls back to restart.
func removeUUIDFromRelay(cfg *config.Config, targetUUID string) error {
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		xrayConf, err := readRelayXrayConfig(client)
		if err != nil {
			return err
		}

		settings, clients, err := relayClients(xrayConf)
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
		if err := xrayAPIRemoveUsers(client, []string{targetUUID}); err != nil {
			slog.Warn("xray API remove failed, restarting xray", "error", err)
			restartRelayXray(client)
		}
		return nil
	})
}

// ensureRelayStats checks the relay's Xray config and adds the stats,
// StatsService, and policy if missing. Returns true if the config was
// patched (Xray needs restart).
func ensureRelayStats(client *gossh.Client) bool {
	xrayConf, err := readRelayXrayConfig(client)
	if err != nil {
		return false
	}

	changed := false

	// Ensure "stats": {} exists.
	if _, ok := xrayConf["stats"]; !ok {
		xrayConf["stats"] = map[string]interface{}{}
		changed = true
	}

	// Ensure "StatsService" is in the API services list.
	api, _ := xrayConf["api"].(map[string]interface{})
	if api == nil {
		api = map[string]interface{}{"tag": "api"}
		xrayConf["api"] = api
		changed = true
	}
	services, _ := api["services"].([]interface{})
	hasStats := false
	for _, s := range services {
		if s == "StatsService" {
			hasStats = true
			break
		}
	}
	if !hasStats {
		api["services"] = append(services, "StatsService")
		changed = true
	}

	// Ensure policy exists with both system and user-level stats.
	policy, _ := xrayConf["policy"].(map[string]interface{})
	if policy == nil {
		policy = map[string]interface{}{}
		xrayConf["policy"] = policy
		changed = true
	}

	// System-level stats (required by some Xray versions for the stats
	// infrastructure to fully initialize).
	system, _ := policy["system"].(map[string]interface{})
	if system == nil {
		system = map[string]interface{}{}
		policy["system"] = system
		changed = true
	}
	for _, key := range []string{"statsInboundUplink", "statsInboundDownlink", "statsOutboundUplink", "statsOutboundDownlink"} {
		if v, _ := system[key].(bool); !v {
			system[key] = true
			changed = true
		}
	}

	// User-level stats.
	levels, _ := policy["levels"].(map[string]interface{})
	if levels == nil {
		levels = map[string]interface{}{}
		policy["levels"] = levels
		changed = true
	}
	level0, _ := levels["0"].(map[string]interface{})
	if level0 == nil {
		level0 = map[string]interface{}{}
		levels["0"] = level0
		changed = true
	}
	for _, key := range []string{"statsUserUplink", "statsUserDownlink", "statsUserOnline"} {
		if v, _ := level0[key].(bool); !v {
			level0[key] = true
			changed = true
		}
	}

	if !changed {
		return false
	}

	slog.Info("patching relay Xray config with stats/policy")
	if err := writeRelayXrayConfig(client, xrayConf); err != nil {
		slog.Warn("failed to patch relay stats config", "error", err)
		return false
	}
	restartRelayXray(client)
	return true
}

// sshThroughServerTunnel opens an SSH connection to the relay using the
// server's already-running Xray tunnel (dokodemo-door on SSHPort+1).
// This is much faster than withRelaySSH since it doesn't create a
// temporary Xray instance.
func (o *Ops) sshThroughServerTunnel(cfg *config.Config, fn func(*gossh.Client) error) error {
	xrayAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Server.SSHPort+1)

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
		User:            cfg.Server.RelaySSHUser,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := gossh.Dial("tcp", xrayAddr, sshCfg)
	if err != nil {
		return fmt.Errorf("SSH to relay via server tunnel: %w", err)
	}
	defer client.Close()
	return fn(client)
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

// EnsureRelayStats patches the relay's Xray config to enable online
// user tracking if it's not already configured. Call once at startup.
// Uses the server's running Xray tunnel for fast access.
func (o *Ops) EnsureRelayStats() {
	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return
	}

	// Brief delay to let the server's Xray tunnel stabilize.
	time.Sleep(3 * time.Second)

	err := o.sshThroughServerTunnel(cfg, func(client *gossh.Client) error {
		patched := ensureRelayStats(client)
		if patched {
			slog.Info("relay stats config patched, Xray restarted")
			return nil
		}

		// Config looks correct — verify stats are actually working by
		// querying the API. If no stats exist, force a restart to ensure
		// the running Xray loaded the config with stats enabled.
		conn, err := dialRelayGRPC(client)
		if err != nil {
			return nil // non-fatal
		}
		defer conn.Close()

		sc := statsCmd.NewStatsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := sc.QueryStats(ctx, &statsCmd.QueryStatsRequest{Pattern: ""})
		if err != nil {
			return nil // non-fatal
		}

		// If the Xray process has been running for a while but has zero
		// stats, the running process likely started before stats were
		// configured. Force a restart.
		if len(resp.GetStat()) == 0 {
			slog.Info("relay Xray has zero stats — restarting to apply stats config")
			restartRelayXray(client)
		}
		return nil
	})
	if err != nil {
		slog.Warn("could not ensure relay stats config", "error", err)
	}
}

