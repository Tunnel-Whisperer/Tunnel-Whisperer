package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalMode(t *testing.T) {
	cases := map[string]string{"admin": "relay", "relay": "relay", "server": "server", "client": "client", "": ""}
	for in, want := range cases {
		if got := CanonicalMode(in); got != want {
			t.Errorf("CanonicalMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadMigratesAdminToRelay(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("mode: admin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "relay" {
		t.Fatalf("loaded mode = %q, want relay", cfg.Mode)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if string(data) == "mode: admin\n" {
		t.Error("config.yaml still says mode: admin — migration was not persisted")
	}
}

func TestValidModeAcceptsRelayNotAdmin(t *testing.T) {
	if !ValidMode("relay") {
		t.Error("relay should be valid")
	}
	if ValidMode("admin") {
		t.Error("admin should no longer be a valid canonical mode")
	}
}

// probedDirs mirrors what CheckWritable probes: Dir() itself plus every
// entry in checkWritableSubdirs.
func probedDirs() []string {
	dir := Dir()
	dirs := []string{dir}
	for _, sub := range checkWritableSubdirs {
		dirs = append(dirs, filepath.Join(dir, sub))
	}
	return dirs
}

func TestCheckWritableSucceedsOnRealDir(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := CheckWritable(); err != nil {
		t.Fatalf("CheckWritable: %v", err)
	}
	// Every probed dir (Dir() itself + contexts/users/servers) must exist,
	// and none may have left its probe file behind.
	for _, d := range probedDirs() {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatalf("probed dir %s was not created: %v", d, err)
		}
		for _, e := range entries {
			if e.Name() == ".tw-write-probe" {
				t.Fatalf("probe file was not cleaned up in %s", d)
			}
		}
	}
}

func TestCheckWritableFailsWhenParentIsAFile(t *testing.T) {
	// A regular file as the PARENT of the config dir makes MkdirAll fail
	// deterministically on any OS/user, without needing to drop privileges.
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TW_CONFIG_DIR", filepath.Join(blocker, "config"))

	err := CheckWritable()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), blocker) {
		t.Errorf("error %q does not mention the unwritable dir %q", err.Error(), blocker)
	}
	if !strings.Contains(err.Error(), "TW_CONFIG_DIR") {
		t.Errorf("error %q does not mention the TW_CONFIG_DIR remedy", err.Error())
	}
}

// TestCheckWritableFailsWhenProbeIsADirectory covers the OTHER failure
// branch: the dir exists (MkdirAll succeeds/no-ops) but the probe file
// create fails. A pre-existing DIRECTORY named exactly ".tw-write-probe"
// makes os.Create fail with EISDIR regardless of privilege — root can't
// open a directory for writing as if it were a file either, so this is
// root-proof, unlike a permission-bit trick.
func TestCheckWritableFailsWhenProbeIsADirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".tw-write-probe"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := CheckWritable()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q does not mention the blocked dir %q", err.Error(), dir)
	}
}

// TestCheckWritableFailsWhenSubdirProbeIsADirectory locks in the fix for
// the spatial gap: Dir() itself can be perfectly writable while a state
// subdirectory (here "servers", the registry dir EnrollServer/AddServer
// write under) is blocked — CheckWritable must still catch that.
func TestCheckWritableFailsWhenSubdirProbeIsADirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	serversDir := filepath.Join(dir, "servers")
	if err := os.MkdirAll(filepath.Join(serversDir, ".tw-write-probe"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := CheckWritable()
	if err == nil {
		t.Fatal("want an error, got nil (subdir probe failure was not caught)")
	}
	if !strings.Contains(err.Error(), serversDir) {
		t.Errorf("error %q does not name the blocked subdir %q", err.Error(), serversDir)
	}
}

// TestDirAbsolutizesRelativeOverride: a relative TW_CONFIG_DIR (e.g. from
// `--config-dir ./clients`) must come back absolute — derived paths are handed
// to xray-core, which resolves relative paths against ITS base, not our CWD.
func TestDirAbsolutizesRelativeOverride(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", "clients")
	got := Dir()
	if !filepath.IsAbs(got) {
		t.Fatalf("Dir() = %q, want absolute", got)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, "clients"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}
