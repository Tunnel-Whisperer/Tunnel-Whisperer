package ops

import (
	"archive/zip"
	"bytes"
	"errors"
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
		role, relay := m.Role, m.Relay
		// The index metadata is only refreshed on a context switch, so for the
		// active context it can be stale (e.g. after `tw admin create` sets admin
		// mode + relay on the live config without touching the cache). The live
		// config is authoritative for the current context.
		if name == idx.CurrentContext {
			cfg := o.Config()
			role, relay = cfg.Mode, cfg.Xray.RelayHost
		}
		out = append(out, ContextInfo{
			Name:    name,
			Role:    role,
			Relay:   relay,
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

// ErrContextExists is returned by ImportContext (with replace=false) when a
// context with the resolved name already exists. The resolved name is returned
// alongside it so callers can prompt before replacing.
var ErrContextExists = errors.New("context already exists")

// ImportContext stores an encrypted profile bundle (a context bundle, or a
// legacy admin/user bundle — same encrypted-zip shape) as a new context. The
// bundle is decrypted once (passphrase) to read its config.yaml for the index
// metadata; the encrypted blob is stored as-is. If name is empty it is derived
// from the bundle's relay domain. Returns the resolved context name.
//
// If a context with the resolved name already exists, ImportContext returns
// that name with ErrContextExists unless replace is true, in which case the
// stored bundle and its metadata are overwritten in place (the existing
// context is updated, not duplicated).
func (o *Ops) ImportContext(bundle []byte, name string, replace bool) (string, error) {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return "", err
	}
	plain, err := cryptobox.Decrypt(bundle, "")
	if err != nil {
		return "", fmt.Errorf("reading bundle (corrupted?): %w", err)
	}
	role, relay := readBundleMeta(plain)
	if name == "" {
		name = sanitizeHostname(relay)
	}
	if _, exists := idx.Contexts[name]; exists && !replace {
		return name, fmt.Errorf("%w: %s", ErrContextExists, name)
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

// ExportCurrentContext seals the live (active) profile into a bundle — the
// portable form of the current context. Bundles carry no passphrase.
func (o *Ops) ExportCurrentContext() ([]byte, error) {
	return sealProfile()
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
// the relay marker, reloads config, and reconnects under the new mode. Bundles
// carry no passphrase.
func (o *Ops) UseContext(name string, progress ProgressFunc) error {
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
	// Capture the departing context's role/relay (from its live config) before we
	// unseal the target over it, so the index metadata stays in sync.
	oldCur := idx.CurrentContext
	var oldRole, oldRelay string
	if oldCur != "" {
		c := o.Config()
		oldRole, oldRelay = c.Mode, c.Xray.RelayHost
	}
	target, err := os.ReadFile(config.ContextBundlePath(name))
	if err != nil {
		return fmt.Errorf("reading target context %q: %w", name, err)
	}

	// 1. Re-seal the current context if its live profile changed.
	if cur := idx.CurrentContext; cur != "" {
		if err := o.resealCurrent(cur); err != nil {
			return err
		}
	}

	// 2. Unseal the target over the live profile.
	if err := unsealProfile(target); err != nil {
		return err
	}

	// 3. Reconcile the relay marker and reload config.
	if err := o.ReloadConfig(); err != nil {
		return fmt.Errorf("reloading config after switch: %w", err)
	}
	if err := o.ensureRelayMarker(); err != nil {
		slog.Warn("switch: could not restore relay marker", "error", err)
	}

	// 4. Update the index pointer (+ refresh the departing context's metadata)
	//    and cache the new passphrase.
	if oldCur != "" {
		if m, ok := idx.Contexts[oldCur]; ok {
			m.Role, m.Relay = oldRole, oldRelay
			idx.Contexts[oldCur] = m
		}
	}
	idx.CurrentContext = name
	if err := config.SaveContextIndex(idx); err != nil {
		return err
	}

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

// ReapplyContext re-unseals the stored bundle of the already-active context
// over the live profile, then reloads config and reconnects. Used after the
// active context's bundle is replaced by an import: re-activating it via
// UseContext is a no-op (it is already current), so the live profile would
// otherwise keep the old config. This ONLY unseals (bundle -> live) and never
// re-seals, so the freshly imported bundle is authoritative and a later switch
// won't write the stale live profile back over it.
func (o *Ops) ReapplyContext(name string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}
	bundle, err := os.ReadFile(config.ContextBundlePath(name))
	if err != nil {
		return fmt.Errorf("reading context %q: %w", name, err)
	}
	if err := unsealProfile(bundle); err != nil {
		return err
	}
	if err := o.ReloadConfig(); err != nil {
		return fmt.Errorf("reloading config after reapply: %w", err)
	}
	if err := o.ensureRelayMarker(); err != nil {
		slog.Warn("reapply: could not restore relay marker", "error", err)
	}

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
// changed since it was sealed. Bundles carry no passphrase, so this always
// succeeds — there is nothing to prompt for.
func (o *Ops) resealCurrent(cur string) error {
	existing, readErr := os.ReadFile(config.ContextBundlePath(cur))
	if readErr != nil {
		// No existing snapshot.
		if liveProfileEmpty() {
			return nil // nothing to preserve (fresh/abandoned empty context)
		}
		return o.writeSealedProfile(cur)
	}
	// Skip re-seal if the live profile matches the sealed one.
	if plain, derr := cryptobox.Decrypt(existing, ""); derr == nil {
		if h, herr := profileHash(); herr == nil && zipHash(plain) == h {
			return nil // unchanged
		}
	}
	return o.writeSealedProfile(cur)
}

// liveProfileEmpty reports whether the active config dir has no profile to
// preserve (no config.yaml).
func liveProfileEmpty() bool {
	_, err := os.Stat(config.FilePath())
	return os.IsNotExist(err)
}

// NewContext seals the current context (preserving it), then starts a FRESH
// EMPTY context and switches to it. The new context is unconfigured — the caller
// sets it up next (e.g. `tw server join`). Bundles carry no passphrase.
func (o *Ops) NewContext(name string) error {
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return err
	}
	if _, exists := idx.Contexts[name]; exists {
		return fmt.Errorf("context already exists: %s", name)
	}
	if cur := idx.CurrentContext; cur != "" {
		c := o.Config()
		oldRole, oldRelay := c.Mode, c.Xray.RelayHost
		if err := o.resealCurrent(cur); err != nil {
			return err
		}
		// The current context MUST be sealed before we wipe the live dir, or it
		// would be lost — unless it was empty (nothing to preserve). Only require
		// a snapshot when the live profile had content.
		if _, err := os.Stat(config.ContextBundlePath(cur)); err != nil && !liveProfileEmpty() {
			return fmt.Errorf("refusing to create a new context: the current context %q could not be sealed", cur)
		}
		if m, ok := idx.Contexts[cur]; ok {
			m.Role, m.Relay = oldRole, oldRelay
			idx.Contexts[cur] = m
		}
	}
	if err := clearLiveProfile(); err != nil {
		return err
	}
	idx.Contexts[name] = config.ContextMeta{Created: time.Now().UTC().Format(time.RFC3339)}
	idx.CurrentContext = name
	if err := config.SaveContextIndex(idx); err != nil {
		return err
	}
	if err := o.ReloadConfig(); err != nil {
		return fmt.Errorf("reloading after new-context: %w", err)
	}
	return nil
}

func (o *Ops) writeSealedProfile(name string) error {
	blob, err := sealProfile()
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
