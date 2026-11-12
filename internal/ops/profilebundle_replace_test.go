package ops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestUnsealProfileReplacesProfileSet: unseal is authoritative for the
// profile set — live profile files the bundle does not carry (a planted
// registry entry, a user dir, a flat key) are gone afterwards, while files
// outside the profile set (the context store, authorized_keys) are kept.
func TestUnsealProfileReplacesProfileSet(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeFile(t, config.FilePath(), "mode: relay\n")
	writeFile(t, config.CAKeyPath(), "OLD-CA-KEY")
	writeFile(t, filepath.Join(RegistryDir(), "evil.json"), `{"server_id":"evil"}`)
	writeFile(t, filepath.Join(config.UsersDir(), "mallory", "config.yaml"), "x")
	writeFile(t, filepath.Join(config.RelayDir(), "stale.tf"), "x")
	writeFile(t, config.AuthorizedKeysPath(), "keep")
	writeFile(t, filepath.Join(config.ContextsDir(), "a.twctx"), "keep")

	bundle := buildSealedBundle(t, map[string]string{
		"config.yaml":          "mode: relay\nxray:\n  uuid: NEW\n",
		"servers/good.json":    `{"server_id":"good"}`,
		"users/alice/user.crt": "CRT",
	})
	if err := unsealProfile(bundle); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"config.yaml":          "mode: relay\nxray:\n  uuid: NEW\n",
		"servers/good.json":    `{"server_id":"good"}`,
		"users/alice/user.crt": "CRT",
		"authorized_keys":      "keep",
		"contexts/a.twctx":     "keep",
	}
	got := snapshotDir(t, config.Dir())
	for k, v := range want {
		if got[filepath.FromSlash(k)] != v {
			t.Errorf("%s = %q, want %q", k, got[filepath.FromSlash(k)], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("live dir has %d files, want exactly %d: %v", len(got), len(want), got)
	}
	if _, err := os.Stat(config.RelayDir()); !os.IsNotExist(err) {
		t.Errorf("relay/ (not in the bundle) survived: %v", err)
	}
}

// TestPlantedRegistryEntryDoesNotSurviveContextSwitch is the C8 persistence
// regression: a servers/<id>.json from a third-party bundle must not stay in
// the live profile after switching back to the operator's own context, nor
// be resealed into it.
func TestPlantedRegistryEntryDoesNotSurviveContextSwitch(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	idx, err := config.EnsureContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	own := idx.CurrentContext

	third := buildSealedBundle(t, map[string]string{
		"config.yaml":       "mode: relay\n",
		"servers/evil.json": `{"server_id":"evil"}`,
	})
	if _, err := o.ImportContext(third, "third", false); err != nil {
		t.Fatal(err)
	}
	if err := o.UseContext("third", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(RegistryDir(), "evil.json")); err != nil {
		t.Fatalf("third-party context not active: %v", err)
	}
	if err := o.UseContext(own, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(RegistryDir(), "evil.json")); !os.IsNotExist(err) {
		t.Fatalf("planted servers/evil.json survived the switch back: %v", err)
	}
	if err := o.resealCurrent(own); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(config.ContextBundlePath(own))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := unsealProfile(blob); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(RegistryDir(), "evil.json")); !os.IsNotExist(err) {
		t.Fatalf("planted entry was resealed into the operator's context: %v", err)
	}
}

// TestUnsealProfileRollsBackOnCommitFailure: a rename failing partway
// through the commit restores every entry already swapped, so the live
// profile is left as it was.
func TestUnsealProfileRollsBackOnCommitFailure(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeFile(t, config.FilePath(), "mode: client\n")
	writeFile(t, config.CACertPath(), "OLD-CA")
	writeFile(t, filepath.Join(config.UsersDir(), "alice", "config.yaml"), "user: alice")
	writeFile(t, filepath.Join(RegistryDir(), "s1.json"), "{}")
	before := snapshotDir(t, config.Dir())

	bundle := buildSealedBundle(t, map[string]string{
		"config.yaml":        "mode: client\nxray:\n  uuid: NEW\n",
		"ca.crt":             "NEW-CA",
		"users/bob/user.crt": "CRT",
	})
	for failAt := 1; failAt <= 6; failAt++ {
		n := 0
		profileRename = func(from, to string) error {
			n++
			if n == failAt {
				return errors.New("injected rename failure")
			}
			return os.Rename(from, to)
		}
		err := unsealProfile(bundle)
		profileRename = os.Rename
		if err == nil {
			t.Fatalf("failAt=%d: want the injected failure, got nil", failAt)
		}
		diffSnapshots(t, before, snapshotDir(t, config.Dir()))
	}
}
