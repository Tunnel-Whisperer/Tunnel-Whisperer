package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config holds all Tunnel Whisperer settings.
type Config struct {
	Mode     string       `yaml:"mode,omitempty"`      // "server" or "client"
	LogLevel string       `yaml:"log_level,omitempty"` // debug, info, warn, error
	Proxy    string       `yaml:"proxy,omitempty"`     // e.g. "socks5://user:pass@host:port" or "http://host:port"
	Xray     XrayConfig   `yaml:"xray"`
	Server   ServerConfig `yaml:"server"`
	Client   ClientConfig `yaml:"client"`
}

// XrayConfig is the shared transport layer (both server and client).
type XrayConfig struct {
	UUID      string `yaml:"uuid"`
	RelayHost string `yaml:"relay_host"`
	RelayPort int    `yaml:"relay_port"`
	Path      string `yaml:"path"`
}

// ServerConfig holds settings only used by `tw serve`.
type ServerConfig struct {
	SSHPort      int    `yaml:"ssh_port"`
	APIPort      int    `yaml:"api_port"`
	DashboardPort int   `yaml:"dashboard_port"`
	RelaySSHPort int    `yaml:"relay_ssh_port"`
	RelaySSHUser string `yaml:"relay_ssh_user"`
	RemotePort   int    `yaml:"remote_port"`
}

// ClientConfig holds settings only used by `tw connect`.
type ClientConfig struct {
	SSHUser       string   `yaml:"ssh_user"`
	ServerSSHPort int      `yaml:"server_ssh_port"`
	Tunnels       []Tunnel `yaml:"tunnels"`
}

// Tunnel defines a single local-port → remote-host:remote-port mapping.
type Tunnel struct {
	LocalPort  int    `yaml:"local_port"`
	RemoteHost string `yaml:"remote_host"`
	RemotePort int    `yaml:"remote_port"`
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
			SSHPort:      2222,
			APIPort:      50051,
			DashboardPort: 8080,
			RelaySSHPort: 22,
			RelaySSHUser: "ubuntu",
			RemotePort:   2222,
		},
		Client: ClientConfig{
			SSHUser:       "tunnel",
			ServerSSHPort: 2222,
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

	return cfg, nil
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

	if err := os.WriteFile(FilePath(), data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}
