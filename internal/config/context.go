package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ContextMeta is the non-secret index entry for one stored context.
type ContextMeta struct {
	Role    string `yaml:"role"`
	Relay   string `yaml:"relay"`
	User    string `yaml:"user,omitempty"` // client contexts only: client.ssh_user
	ID      string `yaml:"id,omitempty"`   // ShortID of the profile's xray.uuid
	Created string `yaml:"created"`
}

// ShortID is the 8-hex-char short form of a profile UUID ("" if unset) — the
// same convention deriveServerID uses for tenant identities.
func ShortID(uuid string) string {
	u := strings.ReplaceAll(uuid, "-", "")
	if len(u) > 8 {
		return u[:8]
	}
	return u
}

// MetaForConfig derives the context-index metadata for a live config.
func MetaForConfig(cfg *Config) (role, relay, user, id string) {
	role, relay = cfg.Mode, cfg.Xray.RelayHost
	if cfg.Mode == "client" {
		user = cfg.Client.SSHUser
	}
	return role, relay, user, ShortID(cfg.Xray.UUID)
}

// ContextIndex is the plaintext catalogue of contexts and the active one.
type ContextIndex struct {
	CurrentContext string                 `yaml:"current-context"`
	Contexts       map[string]ContextMeta `yaml:"contexts"`
}

// ContextsDir holds the encrypted context bundles.
func ContextsDir() string { return filepath.Join(Dir(), "contexts") }

// ContextsIndexPath is the plaintext index file.
func ContextsIndexPath() string { return filepath.Join(Dir(), "contexts.yaml") }

// ContextBundlePath is the encrypted bundle for a named context.
func ContextBundlePath(name string) string {
	return filepath.Join(ContextsDir(), name+".twctx")
}

// LoadContextIndex reads the index, returning an empty (non-nil) one if absent.
func LoadContextIndex() (*ContextIndex, error) {
	data, err := os.ReadFile(ContextsIndexPath())
	if os.IsNotExist(err) {
		return &ContextIndex{Contexts: map[string]ContextMeta{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading context index: %w", err)
	}
	var idx ContextIndex
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing context index: %w", err)
	}
	if idx.Contexts == nil {
		idx.Contexts = map[string]ContextMeta{}
	}
	return &idx, nil
}

// SaveContextIndex writes the index.
func SaveContextIndex(idx *ContextIndex) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := yaml.Marshal(idx)
	if err != nil {
		return fmt.Errorf("marshaling context index: %w", err)
	}
	return os.WriteFile(ContextsIndexPath(), data, 0o644)
}

// EnsureContextIndex returns the index, migrating a legacy single-config install
// into a "default" context on first run (the live config becomes that context;
// no .twctx is sealed yet — that happens on first switch-away).
func EnsureContextIndex() (*ContextIndex, error) {
	idx, err := LoadContextIndex()
	if err != nil {
		return nil, err
	}
	if idx.CurrentContext != "" {
		return idx, nil
	}
	if _, err := os.Stat(FilePath()); err != nil {
		return idx, nil // no config yet; nothing to migrate
	}
	cfg, err := Load()
	if err != nil {
		return nil, fmt.Errorf("loading config for migration: %w", err)
	}
	role, relay, user, id := MetaForConfig(cfg)
	idx.CurrentContext = "default"
	idx.Contexts["default"] = ContextMeta{
		Role:    role,
		Relay:   relay,
		User:    user,
		ID:      id,
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	if err := SaveContextIndex(idx); err != nil {
		return nil, err
	}
	return idx, nil
}
