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
	}
	for _, f := range flat {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	for _, sub := range []string{"relay", "users"} {
		root := filepath.Join(dir, sub)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
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

// relPath converts an absolute profile-file path to its zip name (relative to config.Dir()).
func relPath(abs string) (string, error) {
	rel, err := filepath.Rel(config.Dir(), abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// sealProfile zips the active profile and cryptobox-encrypts it under passphrase.
func sealProfile(passphrase string) ([]byte, error) {
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
	enc, err := cryptobox.Encrypt(buf.Bytes(), passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypting profile: %w", err)
	}
	return enc, nil
}

// unsealProfile decrypts and unzips a profile into config.Dir(), overwriting the
// profile files. It refuses any zip entry that escapes config.Dir() (zip-slip)
// or targets the contexts store.
func unsealProfile(data []byte, passphrase string) error {
	plain, err := cryptobox.Decrypt(data, passphrase)
	if err != nil {
		return fmt.Errorf("decrypting profile: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		return fmt.Errorf("reading profile zip (corrupted?): %w", err)
	}
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
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
