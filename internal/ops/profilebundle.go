package ops

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

// clearLiveProfile removes the active profile from config.Dir() (the flat
// identity files + the relay/users/servers dirs), leaving the context store
// (contexts/ + contexts.yaml) intact. Used to start a fresh empty context.
func clearLiveProfile() error {
	dir := config.Dir()
	flat := []string{
		config.FilePath(), config.CACertPath(), config.CAKeyPath(),
		config.ClientCertPath(), config.ClientKeyPath(),
		filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "id_ed25519.pub"),
		filepath.Join(dir, "relay_host_ed25519"), filepath.Join(dir, "relay_host_ed25519.pub"),
	}
	for _, f := range flat {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", f, err)
		}
	}
	for _, sub := range []string{"relay", "users", "servers"} {
		if err := os.RemoveAll(filepath.Join(dir, sub)); err != nil {
			return fmt.Errorf("removing %s: %w", sub, err)
		}
	}
	return nil
}

// profileFiles returns the absolute paths of the files that make up the active
// context profile, gathered from config.Dir(): the flat identity files plus
// everything under relay/ and users/. The contexts store itself is excluded.
func profileFiles() ([]string, error) {
	dir := config.Dir()
	var files []string
	flat := []string{
		config.FilePath(), config.CACertPath(), config.CAKeyPath(),
		config.ClientCertPath(), config.ClientKeyPath(),
		filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "id_ed25519.pub"),
		// The relay host keypair: the .pub is the pin a second-machine admin
		// needs to verify the relay on the direct port-22 channel (SP-7); the
		// private half lets that machine re-provision the same relay identity.
		filepath.Join(dir, "relay_host_ed25519"), filepath.Join(dir, "relay_host_ed25519.pub"),
	}
	for _, f := range flat {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	for _, sub := range []string{"relay", "users", "servers"} {
		root := filepath.Join(dir, sub)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			rel, err := relPath(p)
			if err != nil {
				return err
			}
			if isBundleExcluded(rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", sub, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

// bundleExcluded lists regenerable Terraform artefacts (zip-name form) that
// are never bundled. Terraform's data dir now lives under config.CacheDir();
// this is defence in depth for profiles created before that split.
var bundleExcluded = map[string]bool{
	"relay/.terraform":               true,
	"relay/terraform.tfstate.backup": true,
}

// isBundleExcluded reports whether a zip-name path is, or lies under, a
// bundleExcluded entry.
func isBundleExcluded(rel string) bool {
	for ex := range bundleExcluded {
		if rel == ex || strings.HasPrefix(rel, ex+"/") {
			return true
		}
	}
	return false
}

// stripExcluded re-seals a sealed bundle without its bundleExcluded entries
// (contexts sealed before the Terraform cache split still carry them). A
// bundle with nothing to exclude is returned unchanged.
func stripExcluded(sealed []byte) ([]byte, error) {
	plain, err := cryptobox.Decrypt(sealed, "")
	if err != nil {
		return nil, fmt.Errorf("decrypting bundle: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		return nil, fmt.Errorf("reading bundle zip (corrupted?): %w", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	stripped := false
	for _, zf := range zr.File {
		if isBundleExcluded(zf.Name) {
			stripped = true
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("reading bundle entry %q: %w", zf.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading bundle entry %q: %w", zf.Name, err)
		}
		w, err := zw.Create(zf.Name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if !stripped {
		return sealed, nil
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing bundle zip: %w", err)
	}
	enc, err := cryptobox.Encrypt(buf.Bytes(), "")
	if err != nil {
		return nil, fmt.Errorf("encrypting bundle: %w", err)
	}
	return enc, nil
}

// relPath converts an absolute profile-file path to its zip name (relative to config.Dir()).
func relPath(abs string) (string, error) {
	rel, err := filepath.Rel(config.Dir(), abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// sealProfile zips the active profile and cryptobox-encrypts it. Bundles carry
// no passphrase (sealed with ""); the framing/magic is kept for format
// stability. The bundle is as sensitive as the keys inside it.
func sealProfile() ([]byte, error) {
	files, err := profileFiles()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, abs := range files {
		name, err := relPath(abs)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing profile zip: %w", err)
	}
	enc, err := cryptobox.Encrypt(buf.Bytes(), "")
	if err != nil {
		return nil, fmt.Errorf("encrypting profile: %w", err)
	}
	return enc, nil
}

// unsealProfile decrypts and unzips a profile into config.Dir(), overwriting
// the profile files. It refuses any zip entry that escapes config.Dir()
// (zip-slip) or targets the contexts store.
//
// Every entry is staged in a scratch directory under config.Dir() first and
// only moved into place once ALL of them staged and validated successfully
// (see the pass-by-pass comments below) — a failure partway through (disk
// full, a structural conflict with what's already on disk, whatever) leaves
// the live profile untouched rather than a mix of old and new files. This
// is deliberately not a single atomic directory swap: config.Dir() is a
// fixed path several other things (notably the contexts store) depend on
// staying put, so it can't be swapped wholesale — see the per-pass comments
// for what guarantee that trade-off still gets you.
func unsealProfile(data []byte) error {
	plain, err := cryptobox.Decrypt(data, "")
	if err != nil {
		return fmt.Errorf("decrypting profile: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		return fmt.Errorf("reading profile zip (corrupted?): %w", err)
	}
	dir := filepath.Clean(config.Dir())

	// Pass 1: validate every entry and collect its config.Dir()-relative
	// path, content, and mode. Write NOTHING here — any rejected entry
	// (zip-slip, excluded context-store entry, read error) aborts the whole
	// unseal with zero side effects, so a malformed bundle can never leave a
	// half-overwritten config.
	type pending struct {
		rel     string // config.Dir()-relative, OS-native separators
		content []byte
		mode    os.FileMode
	}
	var writes []pending
	for _, zf := range zr.File {
		clean := filepath.Clean(zf.Name)
		if clean == "contexts" || clean == "contexts.yaml" || strings.HasPrefix(clean, "contexts"+string(os.PathSeparator)) || strings.HasPrefix(clean, "contexts/") {
			return fmt.Errorf("profile bundle must not contain context-store entry %q", zf.Name)
		}
		dest := filepath.Join(dir, clean)
		if dest != dir && !strings.HasPrefix(dest, dir+string(os.PathSeparator)) {
			return fmt.Errorf("illegal profile entry path %q", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("reading profile entry %q: %w", zf.Name, err)
		}
		// Secrets unpack 0600: private keys, the SSH identity, config.yaml
		// (proxy creds + VLESS UUID), and any token file. Public material
		// (.crt/.pub) stays 0644 (finding #12/SP-12).
		mode := os.FileMode(0o644)
		base := filepath.Base(clean)
		if strings.HasSuffix(clean, ".key") || base == "id_ed25519" ||
			base == "relay_host_ed25519" || base == "config.yaml" ||
			strings.HasSuffix(clean, ".token") {
			mode = 0o600
		}
		writes = append(writes, pending{rel: clean, content: content, mode: mode})
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if len(writes) == 0 {
		return nil
	}

	// Pass 2: stage every entry in a scratch directory UNDER config.Dir()
	// (same filesystem as the live profile, so pass 4's move is a rename,
	// not a copy). A failure here — disk full, permissions changing
	// mid-run, whatever — touches ONLY the staging dir; the live profile is
	// completely untouched. Always cleaned up: on success the files have
	// already been moved OUT of it by pass 4, on failure nothing was ever
	// staged into the live dir to begin with.
	staging, err := os.MkdirTemp(dir, ".profile-staging-")
	if err != nil {
		return fmt.Errorf("creating staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	for _, w := range writes {
		stagePath := filepath.Join(staging, w.rel)
		if err := os.MkdirAll(filepath.Dir(stagePath), 0o755); err != nil {
			return fmt.Errorf("staging %s: %w", w.rel, err)
		}
		if err := os.WriteFile(stagePath, w.content, w.mode); err != nil {
			return fmt.Errorf("staging %s: %w", w.rel, err)
		}
	}

	// Pass 3: every entry staged successfully. Before touching the live
	// profile at all, verify every destination is actually replaceable —
	// this catches a STRUCTURAL conflict (e.g. the live dir already has a
	// directory where the bundle wants a plain file) up front too, not just
	// the content-write failures pass 2 guards against, so "on failure the
	// live dir is untouched" covers this case as well.
	for _, w := range writes {
		dest := filepath.Join(dir, w.rel)
		if fi, statErr := os.Lstat(dest); statErr == nil && fi.IsDir() {
			return fmt.Errorf("cannot install %s: an existing directory is in the way", dest)
		}
		if fi, statErr := os.Lstat(filepath.Dir(dest)); statErr == nil && !fi.IsDir() {
			return fmt.Errorf("cannot install %s: %s exists and is not a directory", dest, filepath.Dir(dest))
		}
	}

	// Pass 4: validated + staged — move each into place. Per-file rename,
	// not a single atomic directory swap, for the config.Dir()-is-fixed
	// reason in the function doc above. Each rename is a metadata-only
	// filesystem operation (no data copy), so this is a much narrower
	// failure window than pass 2's content writes — which is why pass 3
	// tries to rule out the foreseeable failure modes before this loop ever
	// starts. On Windows, renaming over an open file fails; that's the same
	// daemon-must-be-stopped assumption `tw config delete-context` already
	// documents elsewhere in this package (a running daemon holds profile
	// files open).
	for _, w := range writes {
		dest := filepath.Join(dir, w.rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("preparing %s: %w", dest, err)
		}
		if err := os.Rename(filepath.Join(staging, w.rel), dest); err != nil {
			return fmt.Errorf("installing %s: %w", dest, err)
		}
	}
	return nil
}

// zipHash hashes the contents of a profile zip the same way profileHash hashes
// the live files, so the two can be compared to detect "unchanged".
//
// Must replicate the profileHash scheme exactly: SHA256 over, for each entry in
// sorted name order, fmt.Fprintf(h,"%s\n%d\n",name,len(content)) then content.
func zipHash(zipBytes []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(zr.File))
	contents := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		names = append(names, f.Name)
		contents[f.Name] = b
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s\n%d\n", n, len(contents[n]))
		h.Write(contents[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// profileHash returns a stable hash of the active profile files (name+content),
// used to skip re-sealing an unchanged context.
//
// Hash scheme (must be replicated verbatim by any sibling hash, e.g. zipHash):
// SHA256 over, for each profile file in sorted relative-path order:
//
//	fmt.Fprintf(h, "%s\n%d\n", relPath, len(content))
//	h.Write(content)
//
// relPath is the config.Dir()-relative, forward-slash path; len(content) is the
// raw byte length. The result is hex-encoded.
func profileHash() (string, error) {
	files, err := profileFiles()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, abs := range files {
		name, err := relPath(abs)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\n%d\n", name, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
