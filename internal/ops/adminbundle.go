package ops

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

// adminBundleEntry is one file packaged into the admin bundle: its name inside
// the zip and its absolute source path. required entries must exist.
type adminBundleEntry struct {
	name     string
	path     string
	required bool
}

func adminBundleEntries() []adminBundleEntry {
	return []adminBundleEntry{
		{"config.yaml", config.FilePath(), true},
		{"ca.crt", config.CACertPath(), true},
		{"ca.key", config.CAKeyPath(), true},
		{"client.crt", config.ClientCertPath(), true},
		{"client.key", config.ClientKeyPath(), true},
		{"id_ed25519", filepath.Join(config.Dir(), "id_ed25519"), true},
		{"id_ed25519.pub", filepath.Join(config.Dir(), "id_ed25519.pub"), true},
		{"relay/relay-meta.json", filepath.Join(config.RelayDir(), "relay-meta.json"), false},
		{"relay/manual-relay.json", filepath.Join(config.RelayDir(), "manual-relay.json"), false},
	}
}

// bundleDestPath maps a bundle entry name to its absolute destination on import.
// It returns false for any name outside the known layout — a zip-slip guard.
func bundleDestPath(name string) (string, bool) {
	for _, e := range adminBundleEntries() {
		if e.name == name {
			return e.path, true
		}
	}
	return "", false
}

// CreateAdminBundle zips the admin identity (config + CA + client cert + SSH key
// + relay metadata) and returns it encrypted under passphrase. The bundle
// carries private keys and must always be passphrase-protected; the caller
// chooses the output filename (conventionally tw_<domain>_admin.zip).
func (o *Ops) CreateAdminBundle(passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("a passphrase is required to protect the admin bundle")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range adminBundleEntries() {
		data, err := os.ReadFile(e.path)
		if err != nil {
			if os.IsNotExist(err) && !e.required {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", e.name, err)
		}
		w, err := zw.Create(e.name)
		if err != nil {
			return nil, fmt.Errorf("adding %s to bundle: %w", e.name, err)
		}
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("writing %s to bundle: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing bundle zip: %w", err)
	}
	enc, err := cryptobox.Encrypt(buf.Bytes(), passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypting admin bundle: %w", err)
	}
	return enc, nil
}

// ImportAdminBundle decrypts an admin bundle and writes its contents into the
// local config directory, making this machine an admin for the relay the bundle
// describes. Existing files are overwritten (re-attach).
func (o *Ops) ImportAdminBundle(data []byte, passphrase string) error {
	plain, err := cryptobox.Decrypt(data, passphrase)
	if err != nil {
		return fmt.Errorf("decrypting admin bundle: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		return fmt.Errorf("reading bundle zip (corrupted?): %w", err)
	}
	if err := os.MkdirAll(config.Dir(), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	for _, zf := range zr.File {
		dest, ok := bundleDestPath(zf.Name)
		if !ok {
			return fmt.Errorf("unexpected entry in admin bundle: %q", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("opening %s in bundle: %w", zf.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("reading %s from bundle: %w", zf.Name, err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("creating dir for %s: %w", zf.Name, err)
		}
		mode := os.FileMode(0644)
		if strings.HasSuffix(zf.Name, ".key") || zf.Name == "id_ed25519" {
			mode = 0600
		}
		if err := os.WriteFile(dest, content, mode); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
		if err := os.Chmod(dest, mode); err != nil {
			return fmt.Errorf("setting permissions on %s: %w", zf.Name, err)
		}
	}
	if err := o.ReloadConfig(); err != nil {
		return fmt.Errorf("reloading config after import: %w", err)
	}
	// Re-attach should leave this machine managing the bundle's relay: restore
	// the provisioned marker so status/SSH work immediately (carried in the
	// bundle when present, else synthesized from the imported config).
	if err := o.ensureRelayMarker(); err != nil {
		slog.Warn("import: could not restore relay provisioned marker", "error", err)
	}
	return nil
}
