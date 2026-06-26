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

// ImportUserBundle extracts a server-issued user bundle (a plain, unencrypted
// zip of config.yaml + client.crt/client.key + id_ed25519[.pub]) into the active
// config dir so the client can connect, and sets the mode to client. Any entry
// that would escape the config dir is rejected (zip-slip).
func ImportUserBundle(zipData []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("reading bundle zip (corrupted?): %w", err)
	}
	dir := filepath.Clean(config.Dir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	for _, zf := range zr.File {
		clean := filepath.Clean(zf.Name)
		dest := filepath.Join(dir, clean)
		if dest != dir && !strings.HasPrefix(dest, dir+string(os.PathSeparator)) {
			return fmt.Errorf("illegal bundle entry path %q", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(clean, ".key") || filepath.Base(clean) == "id_ed25519" {
			mode = 0o600
		}
		if err := os.WriteFile(dest, content, mode); err != nil {
			return fmt.Errorf("writing %s: %w", clean, err)
		}
		if err := os.Chmod(dest, mode); err != nil {
			return fmt.Errorf("setting mode on %s: %w", clean, err)
		}
	}
	// The user bundle's config.yaml carries no mode; this machine is a client.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading imported config: %w", err)
	}
	cfg.Mode = "client"
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("setting client mode: %w", err)
	}
	return nil
}
