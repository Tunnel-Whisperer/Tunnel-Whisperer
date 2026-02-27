package ops

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// UploadClientConfig extracts a config zip (config.yaml + SSH keys) into the
// config directory and reloads the configuration.
func (o *Ops) UploadClientConfig(zipData []byte) error {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("invalid zip file: %w", err)
	}

	allowed := map[string]bool{
		"config.yaml":    true,
		"id_ed25519":     true,
		"id_ed25519.pub": true,
	}

	dir := config.Dir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if !allowed[name] {
			continue
		}
		// Sanitize: no path traversal.
		if strings.Contains(f.Name, "..") {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("opening %s in zip: %w", name, err)
		}

		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("reading %s from zip: %w", name, err)
		}

		perm := os.FileMode(0644)
		if name == "id_ed25519" {
			perm = 0600
		}

		if err := os.WriteFile(filepath.Join(dir, name), data, perm); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}

	// Reload the config from disk (picks up the uploaded config.yaml),
	// then stamp mode = "client" and persist.
	if err := o.ReloadConfig(); err != nil {
		return fmt.Errorf("reloading config after upload: %w", err)
	}

	o.mu.Lock()
	o.cfg.Mode = "client"
	cfg := *o.cfg
	o.mu.Unlock()

	if err := config.Save(&cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}
