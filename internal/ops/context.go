package ops

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log/slog"
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

// DeleteContext removes a stored context. The current context can be deleted
// only when it is the LAST remaining context — that is a full local reset and
// removes the entire config directory. Deleting the current context while other
// contexts exist is refused (switch first).
func (o *Ops) DeleteContext(name string) error {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return err
	}
	if _, ok := idx.Contexts[name]; !ok {
		return fmt.Errorf("no such context: %s", name)
	}
	if idx.CurrentContext == name {
		if len(idx.Contexts) > 1 {
			return fmt.Errorf("cannot delete the current context %q while other contexts exist; switch to another first", name)
		}
		// Sole context and it is active: full local reset — wipe the config dir.
		o.SetActivePassphrase("")
		if err := os.RemoveAll(config.Dir()); err != nil {
			return fmt.Errorf("could not fully remove the config directory %q (a process may be holding files open — stop the tw service, then remove it manually): %w", config.Dir(), err)
		}
		return nil
	}
	if err := os.Remove(config.ContextBundlePath(name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing bundle: %w", err)
	}
	delete(idx.Contexts, name)
	return config.SaveContextIndex(idx)
}

// ImportContext stores an encrypted profile bundle (a context bundle, or a
// legacy admin/user bundle — same encrypted-zip shape) as a new context. The
// bundle is decrypted once (passphrase) to read its config.yaml for the index
// metadata; the encrypted blob is stored as-is. If name is empty it is derived
// from the bundle's relay domain. Returns the resolved context name.
func (o *Ops) ImportContext(bundle []byte, name, passphrase string) (string, error) {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return "", err
	}
	plain, err := cryptobox.Decrypt(bundle, passphrase)
	if err != nil {
		return "", fmt.Errorf("decrypting bundle (wrong passphrase?): %w", err)
	}
	role, relay := readBundleMeta(plain)
	if name == "" {
		name = sanitizeHostname(relay)
	}
	if _, exists := idx.Contexts[name]; exists {
		return "", fmt.Errorf("context already exists: %s", name)
	}
	if err := os.MkdirAll(config.ContextsDir(), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(config.ContextBundlePath(name), bundle, 0o600); err != nil {
		return "", fmt.Errorf("storing context bundle: %w", err)
	}
	idx.Contexts[name] = config.ContextMeta{
		Role:    role,
		Relay:   relay,
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	if err := config.SaveContextIndex(idx); err != nil {
		return "", err
	}
	return name, nil
}

// ExportContext returns the encrypted bundle bytes for a stored (non-current)
// context. To export the active context, use ExportCurrentContext (it seals the
// live profile, which may have no on-disk snapshot yet).
func (o *Ops) ExportContext(name string) ([]byte, error) {
	data, err := os.ReadFile(config.ContextBundlePath(name))
	if err != nil {
		return nil, fmt.Errorf("reading context %q (not sealed yet?): %w", name, err)
	}
	return data, nil
}

// ExportCurrentContext seals the live (active) profile into an encrypted bundle
// under passphrase — the portable form of the current context, and the single
// bundle format (it supersedes the old admin bundle).
func (o *Ops) ExportCurrentContext(passphrase string) ([]byte, error) {
	return sealProfile(passphrase)
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

// UseContext switches the active context to name: it re-seals the current
// profile (if changed), unseals the target over the live config dir, reconciles
// the relay marker, reloads config, and reconnects under the new mode. The
// current context's passphrase comes from the in-memory cache; if the profile
// changed and nothing is cached, currentPassphrase is required.
func (o *Ops) UseContext(name, targetPassphrase, currentPassphrase string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return err
	}
	if _, ok := idx.Contexts[name]; !ok {
		return fmt.Errorf("no such context: %s", name)
	}
	if name == idx.CurrentContext {
		return nil // already active
	}
	target, err := os.ReadFile(config.ContextBundlePath(name))
	if err != nil {
		return fmt.Errorf("reading target context %q: %w", name, err)
	}

	// 1. Re-seal the current context if its live profile changed.
	if cur := idx.CurrentContext; cur != "" {
		if err := o.resealCurrent(cur, currentPassphrase); err != nil {
			return err
		}
	}

	// 2. Unseal the target over the live profile.
	if err := unsealProfile(target, targetPassphrase); err != nil {
		return err
	}

	// 3. Reconcile the relay marker and reload config.
	if err := o.ReloadConfig(); err != nil {
		return fmt.Errorf("reloading config after switch: %w", err)
	}
	if err := o.ensureRelayMarker(); err != nil {
		slog.Warn("switch: could not restore relay marker", "error", err)
	}

	// 4. Update the index pointer + cache the new passphrase.
	idx.CurrentContext = name
	if err := config.SaveContextIndex(idx); err != nil {
		return err
	}
	o.SetActivePassphrase(targetPassphrase)

	// 5. Reconnect under the new mode (only if a connection is active).
	switch o.Mode() {
	case "server":
		if o.ServerStatus().State == StateRunning {
			return o.RestartServer(progress)
		}
	case "client":
		if o.ClientStatus().State == StateRunning {
			return o.ReconnectClient(progress)
		}
	}
	return nil
}

// resealCurrent writes the live profile back to contexts/<cur>.twctx if it
// changed since it was sealed. Uses the cached passphrase; if none and the
// profile changed, currentPassphrase must be supplied.
func (o *Ops) resealCurrent(cur, currentPassphrase string) error {
	pass := o.activePass()
	if pass == "" {
		pass = currentPassphrase
	}

	existing, readErr := os.ReadFile(config.ContextBundlePath(cur))
	if readErr != nil {
		// No existing snapshot.
		if pass == "" {
			// Migrated legacy context with no bundle and no passphrase: skip sealing.
			slog.Warn("current context not sealed (no passphrase); local changes not saved", "context", cur)
			return nil
		}
		// We have a passphrase but no snapshot yet — seal it now.
		return o.writeSealedProfile(cur, pass)
	}

	// Snapshot exists; skip re-seal if live profile matches the sealed one.
	if pass != "" {
		if plain, derr := cryptobox.Decrypt(existing, pass); derr == nil {
			if h, herr := profileHash(); herr == nil {
				if zipHash(plain) == h {
					return nil // unchanged
				}
			}
		}
	}

	if pass == "" {
		return fmt.Errorf("passphrase required to save changes to the current context %q before switching", cur)
	}
	return o.writeSealedProfile(cur, pass)
}

func (o *Ops) writeSealedProfile(name, pass string) error {
	blob, err := sealProfile(pass)
	if err != nil {
		return fmt.Errorf("re-sealing current context: %w", err)
	}
	if err := os.MkdirAll(config.ContextsDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(config.ContextBundlePath(name), blob, 0o600)
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
