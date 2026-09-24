package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config holds all Tunnel Whisperer settings.
type Config struct {
	Mode      string          `yaml:"mode,omitempty"`       // "server", "client", or "relay"
	LogLevel  string          `yaml:"log_level,omitempty"`  // debug, info, warn, error
	LogFormat string          `yaml:"log_format,omitempty"` // "text" (default) or "json"
	Proxy     string          `yaml:"proxy,omitempty"`      // e.g. "socks5://user:pass@host:port" or "http://host:port"
	Xray      XrayConfig      `yaml:"xray"`
	Server    ServerConfig    `yaml:"server"`
	Client    ClientConfig    `yaml:"client"`
	Analytics AnalyticsConfig `yaml:"analytics,omitempty"`
	ModeAuth  *ModeAuth       `yaml:"mode_auth,omitempty"`
}

// ModeAuth is a detached signature over the profile's (mode, identity),
// making the mode field tamper-evident. See internal/ops/modeauth.
type ModeAuth struct {
	Sig    string `yaml:"sig"`
	Issuer string `yaml:"issuer"`
}

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

// AnalyticsConfig controls bandwidth statistics collection.
type AnalyticsConfig struct {
	Enabled     bool `yaml:"enabled"`                // false by default (opt-in)
	HistorySize int  `yaml:"history_size,omitempty"` // ring buffer size (default 720 = 1h at 5s intervals)
}

// XrayConfig is the shared transport layer (both server and client).
type XrayConfig struct {
	UUID           string `yaml:"uuid"`
	ServerID       string `yaml:"server_id,omitempty"` // tenant identity (<hostname>-<first8(uuid)> at first derivation), persisted so it never changes with the hostname
	RelayHost      string `yaml:"relay_host"`
	RelayPort      int    `yaml:"relay_port"`
	Path           string `yaml:"path"`
	ClientCertPath string `yaml:"client_cert_path,omitempty"` // X.509 client cert presented to relay mTLS gate
	ClientKeyPath  string `yaml:"client_key_path,omitempty"`  // matching private key
}

// ServerConfig holds settings only used by `tw server start`.
type ServerConfig struct {
	SSHPort       int `yaml:"ssh_port"`
	APIPort       int `yaml:"api_port"`
	DashboardPort int `yaml:"dashboard_port"`
	// DashboardListen is the interface the web dashboard binds. Defaults to
	// 127.0.0.1. Binding off-loopback serves the login token and session cookie
	// over cleartext HTTP, so it is refused unless DashboardAllowLAN is set (and
	// even then only behind a TLS terminator on a trusted network).
	DashboardListen string `yaml:"dashboard_listen,omitempty"`
	// DashboardAllowLAN must be set to true to permit an off-loopback
	// DashboardListen. It is the explicit acknowledgement that the dashboard's
	// bearer token/cookie travel in cleartext unless fronted by TLS.
	DashboardAllowLAN bool          `yaml:"dashboard_allow_lan,omitempty"`
	RelaySSHPort    int           `yaml:"relay_ssh_port"`
	RelaySSHUser    string        `yaml:"relay_ssh_user"`
	RemotePort      int           `yaml:"remote_port"`
	XrayPort        int           `yaml:"xray_port,omitempty"`
	TempXrayPort    int           `yaml:"temp_xray_port,omitempty"`
	Applications    []Application `yaml:"applications,omitempty" json:"applications,omitempty"`
}

// PortMapping defines a client-port → server-port pair.
type PortMapping struct {
	ClientPort int `yaml:"client_port" json:"client_port"`
	ServerPort int `yaml:"server_port" json:"server_port"`
}

// Application is a named bundle of port mappings that can be assigned to users.
type Application struct {
	Name     string        `yaml:"name"     json:"name"`
	Mappings []PortMapping `yaml:"mappings" json:"mappings"`
}

// ClientConfig holds settings only used by `tw client connect`.
type ClientConfig struct {
	SSHUser       string `yaml:"ssh_user"`
	ServerSSHPort int    `yaml:"server_ssh_port"`
	// ServerHostKey is the server's SSH host public key (authorized_keys
	// format), pinned when the forward tunnel connects so a compromised relay
	// cannot MITM the end-to-end SSH session. Delivered by enrollment.
	ServerHostKey string `yaml:"server_host_key,omitempty"`
	XrayPort      int    `yaml:"xray_port,omitempty"`
	// ListenAddress is the local interface forwarded tunnels bind to.
	// Defaults to 127.0.0.1; set to 0.0.0.0 to expose tunnels (e.g. in containers).
	ListenAddress string   `yaml:"listen_address,omitempty"`
	Tunnels       []Tunnel `yaml:"tunnels"`
	// PortOverrides maps a tunnel's server port (remote_port) to a
	// client-chosen local port, overriding the admin default in Tunnels.
	// Client-owned: user bundles never ship this field.
	PortOverrides map[int]int `yaml:"port_overrides,omitempty"`
}

// Tunnel defines a single local-port → remote-host:remote-port mapping.
type Tunnel struct {
	LocalPort  int    `yaml:"local_port"`
	RemoteHost string `yaml:"remote_host"`
	RemotePort int    `yaml:"remote_port"`
}

// EffectiveTunnels returns Tunnels with each LocalPort replaced by the
// persisted override (PortOverrides) or, with higher precedence, a runtime
// override (from `tw client connect --map`). Both maps are keyed by server
// port (RemotePort). The receiver's Tunnels slice is not mutated; unknown
// keys are ignored here and rejected by ops-level validation.
func (c ClientConfig) EffectiveTunnels(runtime map[int]int) []Tunnel {
	out := make([]Tunnel, len(c.Tunnels))
	for i, t := range c.Tunnels {
		if p, ok := c.PortOverrides[t.RemotePort]; ok {
			t.LocalPort = p
		}
		if p, ok := runtime[t.RemotePort]; ok {
			t.LocalPort = p
		}
		out[i] = t
	}
	return out
}

// Hash returns a SHA-256 hex digest of the YAML-serialised config.
// Used to detect whether the config has changed since a service started.
func (c *Config) Hash() string {
	data, err := yaml.Marshal(c)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// FileHash returns a SHA-256 hex digest of the raw config file on disk.
// Unlike Hash(), this captures all changes including unknown fields,
// comments, and formatting.
func FileHash() string {
	data, err := os.ReadFile(FilePath())
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		Xray: XrayConfig{
			RelayPort: 443,
			Path:      "/tw",
		},
		Server: ServerConfig{
			SSHPort:       2222,
			APIPort:       50051,
			DashboardPort: 8080,
			RelaySSHPort:  22,
			RelaySSHUser:  "ubuntu",
			RemotePort:    2222,
			TempXrayPort:  59000,
		},
		Client: ClientConfig{
			SSHUser:       "tunnel",
			ServerSSHPort: 2222,
			XrayPort:      54001,
			ListenAddress: "127.0.0.1",
		},
	}
}

// Dir returns the platform-specific config directory.
//
//	Linux:   /etc/tw/config
//	Windows: C:\ProgramData\tw\config
//
// Override with TW_CONFIG_DIR environment variable.
func Dir() string {
	if d := os.Getenv("TW_CONFIG_DIR"); d != "" {
		// Always absolute: derived paths (e.g. the client cert handed to
		// xray-core) are consumed by subsystems that resolve relative paths
		// against their OWN base (xray uses the executable's directory), not
		// our working directory.
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
		return d
	}
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\tw\config`
	}
	return "/etc/tw/config"
}

// FilePath returns the full path to the config file.
func FilePath() string {
	return filepath.Join(Dir(), "config.yaml")
}

// RelayDir returns the path to the relay Terraform directory.
func RelayDir() string {
	return filepath.Join(Dir(), "relay")
}

// CacheDir returns the directory for regenerable data that is never bundled.
func CacheDir() string {
	return filepath.Join(Dir(), "cache")
}

// contextIDRE matches a ShortID: exactly 8 lowercase hex chars.
var contextIDRE = regexp.MustCompile(`^[0-9a-f]{8}$`)

// TerraformDataDir returns the TF_DATA_DIR for the profile with the given
// context id. The id comes from bundle data, so anything that is not an
// 8-hex ShortID is rejected rather than joined into a filesystem path.
func TerraformDataDir(contextID string) (string, error) {
	if !contextIDRE.MatchString(contextID) {
		return "", fmt.Errorf("invalid context id %q: want 8 lowercase hex chars", contextID)
	}
	return filepath.Join(CacheDir(), "terraform", contextID), nil
}

// UsersDir returns the path to the directory containing per-user client configs.
func UsersDir() string {
	return filepath.Join(Dir(), "users")
}

// HostKeyDir returns the directory for SSH host keys (same as config dir).
func HostKeyDir() string {
	return Dir()
}

// AuthorizedKeysPath returns the path to the authorized_keys file.
func AuthorizedKeysPath() string {
	return filepath.Join(Dir(), "authorized_keys")
}

// DashboardTokenPath is the file holding the dashboard login token. Kept in its
// own 0600 file rather than config.yaml so it is not exposed by the
// world-readable config.
func DashboardTokenPath() string {
	return filepath.Join(Dir(), "dashboard.token")
}

// APITokenPath is the file holding the local gRPC control-plane token. Kept in
// its own 0600 file (like the dashboard token) so a local process cannot invoke
// the daemon's privileged RPCs — key/config overwrite, mode flip, user
// deletion, secret reads — merely by reaching loopback.
func APITokenPath() string {
	return filepath.Join(Dir(), "api.token")
}

// CACertPath is the server's CA certificate (PEM). Shared with the relay.
func CACertPath() string { return filepath.Join(Dir(), "ca.crt") }

// CAKeyPath is the server's CA private key (PEM). Never leaves the server.
func CAKeyPath() string { return filepath.Join(Dir(), "ca.key") }

// ClientCertPath is the per-server client certificate (PEM) presented to the
// relay's mTLS gate. Shared by all of this server's clients.
func ClientCertPath() string { return filepath.Join(Dir(), "client.crt") }

// ClientKeyPath is the private key for ClientCertPath.
func ClientKeyPath() string { return filepath.Join(Dir(), "client.key") }

// Load reads the YAML config file from the platform-specific path.
// If the file does not exist, it returns the default configuration.
func Load() (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(FilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if canon := CanonicalMode(cfg.Mode); canon != cfg.Mode {
		cfg.Mode = canon
		_ = Save(cfg) // best-effort in-place migration; read-only dir is not fatal
	}

	return cfg, nil
}

// checkWritableSubdirs are the state subdirectories tw creates under Dir()
// that CheckWritable probes individually, in addition to Dir() itself — a
// subdirectory can have permissions that diverge from its parent (e.g. an
// old root-owned "servers" dir surviving under an otherwise-writable, now
// non-root config dir), so probing only Dir() can pass while a write that
// actually lands under one of these still fails. Names must stay in sync
// with config.ContextsDir/UsersDir and ops.RegistryDir ("servers").
var checkWritableSubdirs = []string{"contexts", "users", "servers"}

// CheckWritable ensures Dir() AND every standard state subdirectory
// (contexts, users, servers) exist and are writable, proving each by
// creating and removing a probe file — a permission-bit check is not
// portable (Windows ACLs don't map to Unix mode bits), so this is the only
// reliable way to know a write will actually succeed.
//
// Call this BEFORE any operation that mints or burns something remote or
// single-use (an invite code, a relay-side enrollment) so a local write
// failure is caught up front instead of stranding that side effect —
// see the `tw join` incident this guards against. Signature is stable
// (no dir argument): callers always mean "the active config dir and its
// subdirectories", never an arbitrary path.
func CheckWritable() error {
	dir := Dir()
	if err := checkDirWritable(dir); err != nil {
		return err
	}
	for _, sub := range checkWritableSubdirs {
		if err := checkDirWritable(filepath.Join(dir, sub)); err != nil {
			return err
		}
	}
	return nil
}

// checkDirWritable is the single-directory probe CheckWritable applies to
// Dir() and each of its state subdirectories.
func checkDirWritable(dir string) error {
	const hint = "run elevated (sudo / administrator) or point TW_CONFIG_DIR (or --config-dir) at a writable location"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("config directory %q is not writable: %w — %s", dir, err, hint)
	}
	probe := filepath.Join(dir, ".tw-write-probe")
	f, err := os.Create(probe)
	if err != nil {
		return fmt.Errorf("config directory %q is not writable: %w — %s", dir, err, hint)
	}
	f.Close()
	_ = os.Remove(probe)
	return nil
}

// Save writes the configuration to the platform-specific YAML file.
func Save(cfg *Config) error {
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// 0600: config.yaml can hold secrets (proxy credentials, the VLESS UUID),
	// so it must not be world-readable (finding #12).
	if err := os.WriteFile(FilePath(), data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	// os.WriteFile does not tighten the mode of an EXISTING file, so an upgrade
	// from a version that wrote 0644 would keep the loose bits — chmod to be
	// sure. (No-op on Windows, where mode bits don't map to ACLs.)
	if err := os.Chmod(FilePath(), 0o600); err != nil {
		return fmt.Errorf("tightening config permissions: %w", err)
	}

	return nil
}
