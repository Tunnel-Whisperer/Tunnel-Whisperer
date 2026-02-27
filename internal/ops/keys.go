package ops

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tunnelwhisperer/tw/internal/config"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// EnsureKeys generates ed25519 SSH keys, seeds authorized_keys, and writes a
// default config if none of these exist yet.
func (o *Ops) EnsureKeys() error {
	if err := os.MkdirAll(config.Dir(), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	privPath := filepath.Join(config.Dir(), "id_ed25519")
	pubPath := filepath.Join(config.Dir(), "id_ed25519.pub")

	if _, err := os.Stat(privPath); err == nil {
		return nil // keys already exist
	}

	slog.Info("generating ed25519 SSH key pair")
	privPEM, pubAuthorized, err := twssh.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generating SSH key pair: %w", err)
	}
	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		return fmt.Errorf("writing private key: %w", err)
	}
	if err := os.WriteFile(pubPath, pubAuthorized, 0644); err != nil {
		return fmt.Errorf("writing public key: %w", err)
	}
	slog.Info("SSH keys written", "dir", config.Dir())

	// Seed authorized_keys with the generated public key.
	akPath := config.AuthorizedKeysPath()
	if _, err := os.Stat(akPath); os.IsNotExist(err) {
		if err := os.WriteFile(akPath, pubAuthorized, 0600); err != nil {
			return fmt.Errorf("writing authorized_keys: %w", err)
		}
		slog.Info("authorized_keys seeded", "path", akPath)
	}

	// Save default config if none exists.
	o.mu.Lock()
	cfg := o.cfg
	o.mu.Unlock()

	if _, err := os.Stat(config.FilePath()); os.IsNotExist(err) {
		if err := config.Save(cfg); err != nil {
			slog.Warn("could not save default config", "error", err)
		} else {
			slog.Info("default config written", "path", config.FilePath())
		}
	}

	return nil
}
