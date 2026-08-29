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
		"client.crt":     true,
		"client.key":     true,
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

		// Cap each entry so a decompression bomb cannot exhaust memory (finding
		// SP-18). A config bundle's files (config.yaml + keys) are a few KB;
		// 1 MiB is a generous ceiling. LimitReader guards against a lying
		// uncompressed-size header.
		const maxEntry = 1 << 20
		data, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
		rc.Close()
		if err != nil {
			return fmt.Errorf("reading %s from zip: %w", name, err)
		}
		if len(data) > maxEntry {
			return fmt.Errorf("%s exceeds the %d-byte limit for a config bundle entry", name, maxEntry)
		}

		// Secrets unpack 0600: config.yaml carries the VLESS UUID + proxy
		// credentials, alongside the private keys and any token file (finding
		// #12 — this import path is a sibling of config.Save/profilebundle).
		perm := os.FileMode(0644)
		if name == "id_ed25519" || strings.HasSuffix(name, ".key") ||
			name == "config.yaml" || strings.HasSuffix(name, ".token") {
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
