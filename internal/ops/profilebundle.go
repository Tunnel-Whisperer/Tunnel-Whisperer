package ops

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

// profileFlatFiles returns the absolute paths of the flat identity files of a
// profile in config.Dir(). Together with profileSubdirs it is the single
// definition of what a profile (and so a sealed bundle) may contain.
func profileFlatFiles() []string {
	dir := config.Dir()
	return []string{
		config.FilePath(), config.CACertPath(), config.CAKeyPath(),
		config.ClientCertPath(), config.ClientKeyPath(),
		filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "id_ed25519.pub"),
		// The relay host keypair: the .pub is the pin a second-machine admin
		// needs to verify the relay on the direct port-22 channel (SP-7); the
		// private half lets that machine re-provision the same relay identity.
		filepath.Join(dir, "relay_host_ed25519"), filepath.Join(dir, "relay_host_ed25519.pub"),
	}
}

// profileSubdirs are the config.Dir() subdirectories whose whole content
// belongs to the profile.
var profileSubdirs = []string{"relay", "users", "servers"}

// isProfileEntry reports whether a zip name is one sealProfile can produce:
// a canonical forward-slash path naming a profileFlatFiles entry or a file
// under one of profileSubdirs.
func isProfileEntry(name string) bool {
	if name == "" || name != path.Clean(name) || path.IsAbs(name) || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for _, abs := range profileFlatFiles() {
		if rel, err := relPath(abs); err == nil && rel == name {
			return true
		}
	}
	for _, sub := range profileSubdirs {
		if strings.HasPrefix(name, sub+"/") {
			return true
		}
	}
	return false
}

// clearLiveProfile removes the active profile from config.Dir() (the flat
// identity files + the relay/users/servers dirs), leaving the context store
// (contexts/ + contexts.yaml) intact. Used to start a fresh empty context.
func clearLiveProfile() error {
	dir := config.Dir()
	for _, f := range profileFlatFiles() {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", f, err)
		}
	}
	for _, sub := range profileSubdirs {
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
	for _, f := range profileFlatFiles() {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	for _, sub := range profileSubdirs {
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

// profileRename is os.Rename; a variable so tests can fail the commit midway.
var profileRename = os.Rename

// unsealProfile decrypts and unzips a profile into config.Dir(), REPLACING the
// live profile: afterwards the profile set (profileFlatFiles + profileSubdirs,
// the same set clearLiveProfile removes) is exactly what the bundle carries.
// A file the bundle does not contain — e.g. a servers/<id>.json or users/<x>/
// planted by a third-party bundle — does not survive a switch back to another
// context (where it would otherwise be resealed into that context). Regenerable
// bundleExcluded artefacts in the live relay/ go with the old profile too.
//
// It refuses any zip entry sealProfile could not have produced
// (isProfileEntry) — so nothing outside the profile file set, such as the
// contexts store or authorized_keys, can be planted.
//
// The new profile is staged (with its final modes) in a scratch directory
// under config.Dir() first; only once ALL entries staged and validated does
// the commit swap each profile subdir and flat file: the live one is renamed
// into the scratch dir and the staged one renamed into place. A failure before
// the commit leaves the live profile untouched; a rename failure during the
// commit rolls back the renames already done. config.Dir() itself is a fixed
// path other things (the contexts store) depend on, so it is not swapped
// wholesale. On Windows renaming an open file fails; that's the same
// daemon-must-be-stopped assumption `tw config delete-context` documents.
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
	// path, content, and mode. Write NOTHING here.
	type pending struct {
		rel     string // config.Dir()-relative, OS-native separators
		content []byte
		mode    os.FileMode
	}
	var writes []pending
	for _, zf := range zr.File {
		// Legacy bundles may still carry Terraform cache artefacts; they are
		// regenerable and never restored.
		if isBundleExcluded(zf.Name) {
			continue
		}
		if !isProfileEntry(zf.Name) {
			return fmt.Errorf("profile bundle contains unexpected entry %q", zf.Name)
		}
		clean := filepath.FromSlash(zf.Name)
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
		// Owner-only by default (keys, config.yaml, tokens, and everything
		// under relay/: cloud-init carries the relay host key, tfvars the
		// cloud API token, tfstate is secret-bearing — SP-12/SP-14). Only
		// public material (.crt/.pub) outside relay/ unpacks 0644.
		mode := os.FileMode(0o600)
		if !strings.HasPrefix(zf.Name, "relay/") &&
			(strings.HasSuffix(zf.Name, ".pub") || strings.HasSuffix(zf.Name, ".crt")) {
			mode = 0o644
		}
		writes = append(writes, pending{rel: clean, content: content, mode: mode})
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// A directory where the bundle wants a flat file is not something tw
	// creates; refuse rather than discard whatever it is.
	for _, w := range writes {
		if strings.ContainsRune(w.rel, os.PathSeparator) {
			continue
		}
		dest := filepath.Join(dir, w.rel)
		if fi, statErr := os.Lstat(dest); statErr == nil && fi.IsDir() {
			return fmt.Errorf("cannot install %s: an existing directory is in the way", dest)
		}
	}

	// Pass 2: stage every entry, with its final mode and 0700 parent dirs,
	// under staging/new (same filesystem as the live profile, so the commit
	// is renames only). A failure here touches only the staging dir, which
	// is always removed — on success it holds just the replaced live files.
	staging, err := os.MkdirTemp(dir, ".profile-staging-")
	if err != nil {
		return fmt.Errorf("creating staging dir: %w", err)
	}
	defer os.RemoveAll(staging)
	newRoot, oldRoot := filepath.Join(staging, "new"), filepath.Join(staging, "old")
	for _, d := range []string{newRoot, oldRoot} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return fmt.Errorf("creating staging dir: %w", err)
		}
	}
	for _, w := range writes {
		stagePath := filepath.Join(newRoot, w.rel)
		if err := os.MkdirAll(filepath.Dir(stagePath), 0o700); err != nil {
			return fmt.Errorf("staging %s: %w", w.rel, err)
		}
		if err := os.WriteFile(stagePath, w.content, w.mode); err != nil {
			return fmt.Errorf("staging %s: %w", w.rel, err)
		}
		if err := os.Chmod(stagePath, w.mode); err != nil {
			return fmt.Errorf("staging %s: %w", w.rel, err)
		}
	}
	// MkdirAll's mode is filtered by the umask; set every staged dir 0700.
	err = filepath.WalkDir(newRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != newRoot {
			return os.Chmod(p, 0o700)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("staging profile dirs: %w", err)
	}

	// Commit: for every member of the profile set, move the live entry (if
	// any) aside and the staged one (if any) into place.
	var rels []string
	for _, f := range profileFlatFiles() {
		rel, err := filepath.Rel(dir, f)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
	}
	rels = append(rels, profileSubdirs...)

	type move struct{ from, to string }
	var done []move
	rename := func(from, to string) error {
		if err := profileRename(from, to); err != nil {
			return err
		}
		done = append(done, move{from, to})
		return nil
	}
	rollback := func(cause error) error {
		for i := len(done) - 1; i >= 0; i-- {
			if err := profileRename(done[i].to, done[i].from); err != nil {
				return fmt.Errorf("%w (rollback failed too, the profile in %s is incomplete: %v)", cause, dir, err)
			}
		}
		return cause
	}
	for _, rel := range rels {
		live, staged := filepath.Join(dir, rel), filepath.Join(newRoot, rel)
		if _, err := os.Lstat(live); err == nil {
			if err := rename(live, filepath.Join(oldRoot, rel)); err != nil {
				return rollback(fmt.Errorf("replacing %s: %w", live, err))
			}
		} else if !os.IsNotExist(err) {
			return rollback(fmt.Errorf("replacing %s: %w", live, err))
		}
		if _, err := os.Lstat(staged); err == nil {
			if err := rename(staged, live); err != nil {
				return rollback(fmt.Errorf("installing %s: %w", live, err))
			}
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
