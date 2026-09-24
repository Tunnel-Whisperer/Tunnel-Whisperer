package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteBundleFile_CwdDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := writeBundleFile("x", []byte("data"), ""); err != nil {
		t.Fatalf("writeBundleFile: %v", err)
	}

	want := filepath.Join(dir, "tw_x.twctx")
	assertBundleFile(t, want, "data")
}

func TestWriteBundleFile_ExistingDir(t *testing.T) {
	dir := t.TempDir()

	if err := writeBundleFile("x", []byte("data"), dir); err != nil {
		t.Fatalf("writeBundleFile: %v", err)
	}

	want := filepath.Join(dir, "tw_x.twctx")
	assertBundleFile(t, want, "data")
}

func TestWriteBundleFile_TrailingSlashDir(t *testing.T) {
	dir := t.TempDir()

	dest := dir + string(os.PathSeparator)
	if err := writeBundleFile("x", []byte("data"), dest); err != nil {
		t.Fatalf("writeBundleFile: %v", err)
	}

	want := filepath.Join(dir, "tw_x.twctx")
	assertBundleFile(t, want, "data")
}

func TestWriteBundleFile_ExactFilePath(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "custom.twctx")

	if err := writeBundleFile("x", []byte("data"), dest); err != nil {
		t.Fatalf("writeBundleFile: %v", err)
	}

	assertBundleFile(t, dest, "data")
}

func TestWriteBundleFile_MissingParentDir(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "missing", "custom.twctx")

	err := writeBundleFile("x", []byte("data"), dest)
	if err == nil {
		t.Fatal("expected an error for a missing parent directory, got nil")
	}
}

func TestWriteBundleFile_OverwriteTightensMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	dest := filepath.Join(t.TempDir(), "custom.twctx")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeBundleFile("x", []byte("data"), dest); err != nil {
		t.Fatalf("writeBundleFile: %v", err)
	}

	assertBundleFile(t, dest, "data")
}

func assertBundleFile(t *testing.T, path, wantData string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(data) != wantData {
		t.Fatalf("data = %q, want %q", data, wantData)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Fatalf("mode = %o, want 0600", mode)
		}
	}
}
