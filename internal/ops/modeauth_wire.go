package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
)

// ProfileIdentity is the exported form of profileIdentity, for callers
// outside package ops (e.g. internal/cli's requireMode).
func ProfileIdentity() (string, error) {
	return profileIdentity()
}

// profileIdentity returns this profile's identity string for mode signing:
// the trimmed contents of id_ed25519.pub.
func profileIdentity() (string, error) {
	b, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		return "", fmt.Errorf("reading profile identity: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func profilePrivPEM() ([]byte, error) {
	return os.ReadFile(filepath.Join(config.Dir(), "id_ed25519"))
}

// stampModeAuth signs the config's current (canonical) mode with this
// profile's own key and stores the result in cfg.ModeAuth. Used where a
// profile self-signs its mode (the relay).
func (o *Ops) stampModeAuth(cfg *config.Config) error {
	priv, err := profilePrivPEM()
	if err != nil {
		return fmt.Errorf("reading profile key: %w", err)
	}
	id, err := profileIdentity()
	if err != nil {
		return err
	}
	sig, issuer, err := modeauth.Sign(priv, config.CanonicalMode(cfg.Mode), id)
	if err != nil {
		return err
	}
	cfg.ModeAuth = &config.ModeAuth{Sig: sig, Issuer: issuer}
	return nil
}

// StampAndSaveModeAuth loads the current config, signs its canonical mode
// with this profile's own key (via stampModeAuth), and persists the result.
// Used for relay self-heal when mode_auth is missing on a set mode.
func (o *Ops) StampAndSaveModeAuth() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := o.stampModeAuth(cfg); err != nil {
		return err
	}
	return config.Save(cfg)
}
