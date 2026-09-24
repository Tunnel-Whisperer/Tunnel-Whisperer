package ops

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSealUnsealProfileRoundTrip(t *testing.T) {
	src := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", src)
	writeFile(t, config.FilePath(), "mode: admin\n")
	writeFile(t, config.CACertPath(), "CA")
	writeFile(t, filepath.Join(config.RelayDir(), "manual-relay.json"), `{"domain":"a"}`)
	writeFile(t, filepath.Join(config.Dir(), "users", "alice", "config.yaml"), "user: alice")

	sealed, err := sealProfile()
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dst)
	if err := unsealProfile(sealed); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{config.FilePath(), config.CACertPath(),
		filepath.Join(config.RelayDir(), "manual-relay.json"),
		filepath.Join(config.Dir(), "users", "alice", "config.yaml")} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing after unseal: %s (%v)", f, err)
		}
	}
}

// TestZipHashMatchesProfileHash pins the invariant that zipHash (over a sealed
// profile zip) and profileHash (over the live files) produce the same value.
// If they ever drift out of the same byte-format, the "unchanged → skip
// re-seal" check in resealCurrent would silently never fire.
func TestZipHashMatchesProfileHash(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeFile(t, config.FilePath(), "mode: admin\n")
	writeFile(t, config.CACertPath(), "CA")
	writeFile(t, filepath.Join(config.Dir(), "users", "alice", "config.yaml"), "user: alice")

	sealed, err := sealProfile()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cryptobox.Decrypt(sealed, "")
	if err != nil {
		t.Fatal(err)
	}
	want, err := profileHash()
	if err != nil {
		t.Fatal(err)
	}
	if got := zipHash(plain); got != want {
		t.Errorf("zipHash = %q, profileHash = %q; must match", got, want)
	}
}

// buildSealedBundle zips entries (relative-path -> content) and cryptobox-
// encrypts them with no passphrase, the same shape sealProfile produces —
// but lets the test hand unsealProfile arbitrary/conflicting entries that
// sealProfile itself would never generate from a real live profile.
func buildSealedBundle(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sealed, err := cryptobox.Encrypt(buf.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// snapshotDir reads every regular file under root and returns rel-path ->
// content, skipping the ".profile-staging-*" scratch dirs unsealProfile
// itself creates and cleans up (a leftover would only appear on a bug we
// want this test to have already failed on for a different reason).
func snapshotDir(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		snap[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func diffSnapshots(t *testing.T, before, after map[string]string) {
	t.Helper()
	var names []string
	seen := map[string]bool{}
	for k := range before {
		names = append(names, k)
		seen[k] = true
	}
	for k := range after {
		if !seen[k] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, bok := before[name]
		a, aok := after[name]
		if bok != aok {
			t.Errorf("%s: present before=%v present after=%v (live profile was NOT left untouched)", name, bok, aok)
			continue
		}
		if bok && b != a {
			t.Errorf("%s: content changed (live profile was NOT left untouched)\n before: %q\n after:  %q", name, b, a)
		}
	}
}

// TestUnsealProfileLeavesLiveProfileUntouchedOnMidUnsealFailure injects a
// failure mid-unseal — the bundle's "id_ed25519" entry wants a plain file
// where the live profile already has a DIRECTORY of that name, a structural
// conflict pass 3 catches before any live file is touched — and asserts the
// live profile comes out byte-for-byte identical to how it went in. This is
// the regression test for the "losing the server config forever" failure
// mode: a mid-unseal failure must never leave a mixed old/new profile.
func TestUnsealProfileLeavesLiveProfileUntouchedOnMidUnsealFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)

	// Seed a live profile with known-good content.
	writeFile(t, config.FilePath(), "mode: client\nxray:\n  uuid: keep-me\n")
	writeFile(t, filepath.Join(config.Dir(), "users", "alice", "config.yaml"), "user: alice")
	// The conflict: id_ed25519 is a DIRECTORY here, not a file.
	if err := os.MkdirAll(filepath.Join(config.Dir(), "id_ed25519"), 0o755); err != nil {
		t.Fatal(err)
	}

	before := snapshotDir(t, config.Dir())

	bundle := buildSealedBundle(t, map[string]string{
		"config.yaml": "mode: client\nxray:\n  uuid: NEW-AND-UNWANTED\n",
		"id_ed25519":  "brand-new-key-material",
	})

	err := unsealProfile(bundle)
	if err == nil {
		t.Fatal("want an error (id_ed25519 is blocked by an existing directory), got nil")
	}

	after := snapshotDir(t, config.Dir())
	diffSnapshots(t, before, after)

	// The conflict path itself must still be exactly what it was: a directory.
	fi, statErr := os.Stat(filepath.Join(config.Dir(), "id_ed25519"))
	if statErr != nil || !fi.IsDir() {
		t.Fatalf("id_ed25519 is no longer the pre-existing directory: stat=%v isDir=%v", statErr, statErr == nil && fi.IsDir())
	}

	// No staging directory should survive a failed unseal.
	entries, err := os.ReadDir(config.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && filepath.Base(e.Name()) != "users" && filepath.Base(e.Name()) != "id_ed25519" {
			t.Errorf("unexpected leftover entry in config.Dir(): %s", e.Name())
		}
	}
}

func TestProfileBundleSkipsTerraformArtefacts(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeFile(t, config.FilePath(), "mode: admin\n")
	writeFile(t, filepath.Join(config.RelayDir(), "manual-relay.json"), `{"domain":"a"}`)
	writeFile(t, filepath.Join(config.RelayDir(), ".terraform", "providers", "x", "provider.bin"), "bin")
	writeFile(t, filepath.Join(config.RelayDir(), "terraform.tfstate.backup"), "{}")

	sealed, err := sealProfile()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cryptobox.Decrypt(sealed, "")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["relay/manual-relay.json"] {
		t.Errorf("relay/manual-relay.json missing from bundle: %v", names)
	}
	for n := range names {
		if strings.HasPrefix(n, "relay/.terraform/") || n == "relay/terraform.tfstate.backup" {
			t.Errorf("bundle must not contain %s", n)
		}
	}
}
