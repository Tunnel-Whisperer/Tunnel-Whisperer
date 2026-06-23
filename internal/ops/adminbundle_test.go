package ops

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func seedIdentity(t *testing.T, dir string) {
	t.Helper()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.yaml", "mode: admin\nxray:\n  relay_host: relay.example.com\n")
	write("ca.crt", "CA-CERT")
	write("ca.key", "CA-KEY")
	write("client.crt", "CLIENT-CERT")
	write("client.key", "CLIENT-KEY")
	write("id_ed25519", "SSH-PRIV")
	write("id_ed25519.pub", "SSH-PUB")
	write("relay/relay-meta.json", `{"name":"relay-test"}`)
}

func TestAdminBundleRoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", srcDir)
	seedIdentity(t, srcDir)

	o, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	enc, err := o.CreateAdminBundle("pw123")
	if err != nil {
		t.Fatalf("CreateAdminBundle: %v", err)
	}

	dstDir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dstDir)
	o2, err := New()
	if err != nil {
		t.Fatalf("New(dst): %v", err)
	}
	if err := o2.ImportAdminBundle(enc, "pw123"); err != nil {
		t.Fatalf("ImportAdminBundle: %v", err)
	}

	for _, rel := range []string{
		"config.yaml", "ca.crt", "ca.key", "client.crt", "client.key",
		"id_ed25519", "id_ed25519.pub", "relay/relay-meta.json",
	} {
		got, err := os.ReadFile(filepath.Join(dstDir, rel))
		if err != nil {
			t.Fatalf("missing %s in dst: %v", rel, err)
		}
		want, err := os.ReadFile(filepath.Join(srcDir, rel))
		if err != nil {
			t.Fatalf("reading source %s: %v", rel, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s mismatch: got %q want %q", rel, got, want)
		}
	}
}

func TestImportAdminBundleWrongPassphrase(t *testing.T) {
	srcDir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", srcDir)
	seedIdentity(t, srcDir)
	o, _ := New()
	enc, err := o.CreateAdminBundle("right")
	if err != nil {
		t.Fatalf("CreateAdminBundle: %v", err)
	}
	dstDir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dstDir)
	o2, _ := New()
	if err := o2.ImportAdminBundle(enc, "wrong"); err == nil {
		t.Fatal("expected wrong-passphrase failure")
	}
}

func TestCreateAdminBundleEmptyPassphrase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	seedIdentity(t, dir)
	o, _ := New()
	if _, err := o.CreateAdminBundle(""); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}
