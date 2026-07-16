package ops

import (
	"errors"
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

// seedContext builds an encrypted profile bundle in a scratch temp dir and
// installs it into the active TW_CONFIG_DIR as a named context. The active
// TW_CONFIG_DIR is whatever is set when seedContext is called.
func seedContext(t *testing.T, name, mode, relay, pass string) {
	t.Helper()
	// Record the active dir so we can restore it after building the bundle.
	activeDir := config.Dir()

	// Build the bundle in an isolated scratch dir so profileFiles() only
	// picks up the one config.yaml we write.
	scratch := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", scratch)
	writeFile(t, config.FilePath(), "mode: "+mode+"\nxray:\n  relay_host: "+relay+"\n")
	_ = pass // bundles carry no passphrase
	blob, err := sealProfile()
	if err != nil {
		t.Fatal(err)
	}

	// Restore the active config dir before touching the index/bundle store.
	t.Setenv("TW_CONFIG_DIR", activeDir)

	if err := os.MkdirAll(config.ContextsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ContextBundlePath(name), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, _ := config.LoadContextIndex()
	idx.Contexts[name] = config.ContextMeta{Role: mode, Relay: relay}
	if idx.CurrentContext == "" {
		idx.CurrentContext = name
	}
	if err := config.SaveContextIndex(idx); err != nil {
		t.Fatal(err)
	}
	_ = cryptobox.Encrypt // keep import
}

func newOpsForTest(t *testing.T) *Ops {
	t.Helper()
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, config.FilePath(), "mode: admin\n")
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestListAndDeleteContexts(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	seedContext(t, "relay-a", "admin", "a.example.com", "pw")
	seedContext(t, "relay-b", "client", "b.example.com", "pw")

	list, err := o.ListContexts()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 contexts, got %d", len(list))
	}
	// Current cannot be deleted.
	cur, _ := o.CurrentContext()
	if err := o.DeleteContext(cur); err == nil {
		t.Error("deleting current context should fail")
	}
	// A non-current one can be deleted.
	other := "relay-b"
	if cur == "relay-b" {
		other = "relay-a"
	}
	if err := o.DeleteContext(other); err != nil {
		t.Fatalf("delete %s: %v", other, err)
	}
	if _, err := os.Stat(config.ContextBundlePath(other)); !os.IsNotExist(err) {
		t.Error("context bundle not removed on delete")
	}
	// Verify index was updated too.
	idx, err := config.LoadContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	if _, found := idx.Contexts[other]; found {
		t.Errorf("deleted context %q still present in index", other)
	}
}

func TestDeleteLastContextWipesConfig(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	// One context, which is current.
	seedContext(t, "only", "admin", "a.example.com", "pw")
	if _, err := os.Stat(config.FilePath()); err != nil {
		t.Fatalf("config.yaml should exist before delete: %v", err)
	}
	// Deleting the sole, current context wipes the whole config dir (full reset).
	if err := o.DeleteContext("only"); err != nil {
		t.Fatalf("DeleteContext: %v", err)
	}
	if _, err := os.Stat(config.Dir()); !os.IsNotExist(err) {
		t.Errorf("config dir should be removed after deleting the last context; stat err = %v", err)
	}
}

func TestDeleteCurrentWithOthersRefused(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	seedContext(t, "a", "admin", "a.example.com", "pw")  // becomes current
	seedContext(t, "b", "client", "b.example.com", "pw") // second context
	cur, _ := o.CurrentContext()
	if err := o.DeleteContext(cur); err == nil {
		t.Error("deleting the current context while others exist should be refused")
	}
	if _, err := os.Stat(config.Dir()); err != nil {
		t.Errorf("config dir must NOT be wiped when the delete is refused: %v", err)
	}
}

func TestNewContextPreservesCurrent(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	if _, err := config.EnsureContextIndex(); err != nil { // migrate "default"
		t.Fatal(err)
	}
	// Create a fresh empty context, preserving "default" (sealed, no passphrase).
	if err := o.NewContext("srv"); err != nil {
		t.Fatal(err)
	}
	// The previous context is preserved as a sealed snapshot.
	if _, err := os.Stat(config.ContextBundlePath("default")); err != nil {
		t.Errorf("default context not sealed/preserved: %v", err)
	}
	// The new context is current and its live profile is empty.
	if cur, _ := o.CurrentContext(); cur != "srv" {
		t.Errorf("current = %q, want srv", cur)
	}
	if _, err := os.Stat(config.FilePath()); !os.IsNotExist(err) {
		t.Errorf("live config.yaml should be wiped for the fresh context: %v", err)
	}
	// Both contexts exist (current one not deleted).
	if list, _ := o.ListContexts(); len(list) != 2 {
		t.Fatalf("want 2 contexts, got %d", len(list))
	}
}

func TestUseContextSwitchesActive(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	idx, _ := config.EnsureContextIndex() // migrates "default"
	_ = idx
	// Seed a second context to switch to.
	seedContext(t, "relay-b", "client", "b.example.com", "pw")

	// Switching preserves the content-bearing "default" (sealed, no passphrase)
	// and proceeds without any prompt.
	if err := o.UseContext("relay-b", func(ProgressEvent) {}); err != nil {
		t.Fatal(err)
	}
	// "default" is preserved as a sealed snapshot.
	if _, err := os.Stat(config.ContextBundlePath("default")); err != nil {
		t.Errorf("default context not preserved on switch: %v", err)
	}
	cur, _ := o.CurrentContext()
	if cur != "relay-b" {
		t.Fatalf("current = %q, want relay-b", cur)
	}
	// The active config.yaml is now relay-b's (mode client).
	cfg, _ := config.Load()
	if cfg.Mode != "client" {
		t.Errorf("active mode = %q, want client", cfg.Mode)
	}
}

func TestImportContext(t *testing.T) {
	activeDir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", activeDir)
	o := newOpsForTest(t)

	// Build the import bundle in a separate scratch dir so profileFiles()
	// only captures the one config.yaml we care about.
	scratch := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", scratch)
	writeFile(t, config.FilePath(), "mode: client\nxray:\n  relay_host: imp.example.com\n")
	blob, err := sealProfile()
	if err != nil {
		t.Fatal(err)
	}

	// Restore the active dir before calling ImportContext so the index and
	// bundle store are written to the correct location.
	t.Setenv("TW_CONFIG_DIR", activeDir)

	if name, err := o.ImportContext(blob, "imported", false); err != nil {
		t.Fatalf("ImportContext: %v", err)
	} else if name != "imported" {
		t.Fatalf("ImportContext returned name %q, want imported", name)
	}

	// Re-importing the same name without replace is refused (ErrContextExists),
	// returning the resolved name so callers can prompt.
	if name, err := o.ImportContext(blob, "imported", false); !errors.Is(err, ErrContextExists) {
		t.Fatalf("re-import: got (%q, %v), want ErrContextExists", name, err)
	} else if name != "imported" {
		t.Fatalf("re-import returned name %q, want imported", name)
	}
	// With replace=true it succeeds (updates in place, no duplicate).
	if _, err := o.ImportContext(blob, "imported", true); err != nil {
		t.Fatalf("replace import: %v", err)
	}

	// The encrypted bundle must be on disk.
	if _, err := os.Stat(config.ContextBundlePath("imported")); err != nil {
		t.Errorf("bundle not stored: %v", err)
	}

	// The index must have the correct metadata extracted from config.yaml.
	idx, err := config.LoadContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := idx.Contexts["imported"]
	if !ok {
		t.Fatal("imported context not in index")
	}
	if meta.Role != "client" {
		t.Errorf("role = %q, want client", meta.Role)
	}
	if meta.Relay != "imp.example.com" {
		t.Errorf("relay = %q, want imp.example.com", meta.Relay)
	}
}
