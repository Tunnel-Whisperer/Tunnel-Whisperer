package ops

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
	"gopkg.in/yaml.v3"
)

// ContextInfo is one row of `tw config get-contexts`.
type ContextInfo struct {
	Name    string
	Role    string
	Relay   string
	Current bool
}

// ListContexts returns the stored contexts (migrating a legacy install first).
func (o *Ops) ListContexts() ([]ContextInfo, error) {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return nil, err
	}
	var out []ContextInfo
	for name, m := range idx.Contexts {
		out = append(out, ContextInfo{
			Name:    name,
			Role:    m.Role,
			Relay:   m.Relay,
			Current: name == idx.CurrentContext,
		})
	}
	return out, nil
}

// CurrentContext returns the active context name.
func (o *Ops) CurrentContext() (string, error) {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return "", err
	}
	return idx.CurrentContext, nil
}

// RenameContext renames a stored context (and its bundle file).
func (o *Ops) RenameContext(oldName, newName string) error {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return err
	}
	meta, ok := idx.Contexts[oldName]
	if !ok {
		return fmt.Errorf("no such context: %s", oldName)
	}
	if _, exists := idx.Contexts[newName]; exists {
		return fmt.Errorf("context already exists: %s", newName)
	}
	if _, err := os.Stat(config.ContextBundlePath(oldName)); err == nil {
		if err := os.Rename(config.ContextBundlePath(oldName), config.ContextBundlePath(newName)); err != nil {
			return fmt.Errorf("renaming bundle: %w", err)
		}
	}
	delete(idx.Contexts, oldName)
	idx.Contexts[newName] = meta
	if idx.CurrentContext == oldName {
		idx.CurrentContext = newName
	}
	return config.SaveContextIndex(idx)
}

// DeleteContext removes a stored context. The current context cannot be deleted.
func (o *Ops) DeleteContext(name string) error {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return err
	}
	if _, ok := idx.Contexts[name]; !ok {
		return fmt.Errorf("no such context: %s", name)
	}
	if idx.CurrentContext == name {
		return fmt.Errorf("cannot delete the current context %q; switch first", name)
	}
	if err := os.Remove(config.ContextBundlePath(name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing bundle: %w", err)
	}
	delete(idx.Contexts, name)
	return config.SaveContextIndex(idx)
}

// ImportContext stores an encrypted profile bundle as a new context. The bundle
// is decrypted once (passphrase) to read its config.yaml for the index metadata;
// the encrypted blob is stored as-is.
func (o *Ops) ImportContext(bundle []byte, name, passphrase string) error {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return err
	}
	if _, exists := idx.Contexts[name]; exists {
		return fmt.Errorf("context already exists: %s", name)
	}
	plain, err := cryptobox.Decrypt(bundle, passphrase)
	if err != nil {
		return fmt.Errorf("decrypting bundle (wrong passphrase?): %w", err)
	}
	role, relay := readBundleMeta(plain)
	if err := os.MkdirAll(config.ContextsDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(config.ContextBundlePath(name), bundle, 0o600); err != nil {
		return fmt.Errorf("storing context bundle: %w", err)
	}
	idx.Contexts[name] = config.ContextMeta{
		Role:    role,
		Relay:   relay,
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	return config.SaveContextIndex(idx)
}

// ExportContext returns the encrypted bundle bytes for a stored context.
func (o *Ops) ExportContext(name string) ([]byte, error) {
	data, err := os.ReadFile(config.ContextBundlePath(name))
	if err != nil {
		return nil, fmt.Errorf("reading context %q (not sealed yet?): %w", name, err)
	}
	return data, nil
}

// readBundleMeta extracts mode + relay host from a decrypted profile zip's
// config.yaml for indexing. Best-effort: returns ("","") if not parseable.
func readBundleMeta(plainZip []byte) (role, relay string) {
	type miniXray struct {
		RelayHost string `yaml:"relay_host"`
	}
	type miniCfg struct {
		Mode string   `yaml:"mode"`
		Xray miniXray `yaml:"xray"`
	}
	data, err := readZipEntry(plainZip, "config.yaml")
	if err != nil {
		return "", ""
	}
	var c miniCfg
	if yaml.Unmarshal(data, &c) != nil {
		return "", ""
	}
	return c.Mode, c.Xray.RelayHost
}

// readZipEntry reads one named entry from an in-memory zip.
func readZipEntry(zipBytes []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("entry %q not found", name)
}
