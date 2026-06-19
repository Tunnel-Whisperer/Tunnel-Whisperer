package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/pki"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// EnsureKeys generates ed25519 SSH keys, the per-server CA and client cert
// (server installs only), seeds authorized_keys, and writes a default config if
// none of these exist yet.
func (o *Ops) EnsureKeys() error {
	if err := os.MkdirAll(config.Dir(), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := o.ensureCerts(); err != nil {
		return fmt.Errorf("ensuring certificates: %w", err)
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

// ensureCerts generates the per-server CA and issues this server's client
// certificate (presented to the relay's mTLS gate) if they don't exist yet.
// It is skipped on client installs: clients receive their client cert from the
// server via the config bundle and must never overwrite it with a self-signed
// one. Idempotent and self-healing — an existing CA is never regenerated, but a
// missing client cert is re-issued from the existing CA.
func (o *Ops) ensureCerts() error {
	o.mu.Lock()
	mode := o.cfg.Mode
	host := o.cfg.Xray.RelayHost
	o.mu.Unlock()

	if mode == "client" {
		return nil
	}

	id := host
	if id == "" {
		id = "tw-server"
	}

	caExists, err := statExists(config.CACertPath())
	if err != nil {
		return fmt.Errorf("checking CA certificate: %w", err)
	}
	clientExists, err := statExists(config.ClientCertPath())
	if err != nil {
		return fmt.Errorf("checking client certificate: %w", err)
	}
	if caExists && clientExists {
		return nil
	}

	var caCertPEM, caKeyPEM []byte
	if caExists {
		if caCertPEM, err = os.ReadFile(config.CACertPath()); err != nil {
			return fmt.Errorf("reading CA certificate: %w", err)
		}
		if caKeyPEM, err = os.ReadFile(config.CAKeyPath()); err != nil {
			return fmt.Errorf("reading CA key: %w", err)
		}
	} else {
		slog.Info("generating server CA", "id", id)
		if caCertPEM, caKeyPEM, err = pki.GenerateCA(id); err != nil {
			return fmt.Errorf("generating CA: %w", err)
		}
		if err := os.WriteFile(config.CACertPath(), caCertPEM, 0644); err != nil {
			return fmt.Errorf("writing CA certificate: %w", err)
		}
		if err := os.WriteFile(config.CAKeyPath(), caKeyPEM, 0600); err != nil {
			return fmt.Errorf("writing CA key: %w", err)
		}
	}

	if !clientExists {
		clientCertPEM, clientKeyPEM, err := pki.IssueClientCert(caCertPEM, caKeyPEM, id)
		if err != nil {
			return fmt.Errorf("issuing client certificate: %w", err)
		}
		if err := os.WriteFile(config.ClientCertPath(), clientCertPEM, 0644); err != nil {
			return fmt.Errorf("writing client certificate: %w", err)
		}
		if err := os.WriteFile(config.ClientKeyPath(), clientKeyPEM, 0600); err != nil {
			return fmt.Errorf("writing client key: %w", err)
		}
		slog.Info("server client certificate written", "dir", config.Dir())
	}
	return nil
}

// applyClientCertPaths points the Xray config at this host's local client
// cert/key when they exist on disk and no explicit path is already set. Both
// server (cert from ensureCerts) and client (cert extracted from the config
// bundle) call this at startup so they present the per-server cert at the
// relay's mTLS gate — derived at runtime so a bundle works regardless of
// platform or TW_CONFIG_DIR. Not persisted.
func applyClientCertPaths(xc *config.XrayConfig) {
	if xc.ClientCertPath != "" {
		return
	}
	if _, err := os.Stat(config.ClientCertPath()); err == nil {
		xc.ClientCertPath = config.ClientCertPath()
		xc.ClientKeyPath = config.ClientKeyPath()
	}
}

// statExists reports whether path exists, distinguishing "not found" (false,
// nil) from unexpected stat errors (false, err).
func statExists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}
