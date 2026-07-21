package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	idx := &ContextIndex{
		CurrentContext: "relay-a",
		Contexts: map[string]ContextMeta{
			"relay-a": {Role: "admin", Relay: "a.example.com", Created: "2026-06-25T00:00:00Z"},
		},
	}
	if err := SaveContextIndex(idx); err != nil {
		t.Fatal(err)
	}
	got, err := LoadContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentContext != "relay-a" || got.Contexts["relay-a"].Role != "admin" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestLoadContextIndexMissing(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	got, err := LoadContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got.Contexts == nil {
		t.Error("expected non-nil Contexts map for missing index")
	}
}

func TestEnsureContextIndexMigratesLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Mode = "admin"
	cfg.Xray.RelayHost = "a.example.com"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	idx, err := EnsureContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	// Migration names the context from the live config (admin + relay), not
	// the old hardcoded "default".
	if idx.CurrentContext != "admin-a" {
		t.Fatalf("current = %q, want admin-a", idx.CurrentContext)
	}
	m, ok := idx.Contexts["admin-a"]
	if !ok || m.Role != "admin" || m.Relay != "a.example.com" {
		t.Fatalf("admin-a meta wrong: %+v", idx.Contexts)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "contexts.yaml")); err != nil {
		t.Errorf("index not written: %v", err)
	}
}

func TestDefaultContextName(t *testing.T) {
	cases := []struct{ role, relay, user, want string }{
		{"client", "hds-t2.mint-tunnel.com", "server-1-user", "server-1-user"},
		{"client", "hds-t2.mint-tunnel.com", "Alice B", "alice-b"}, // sanitized
		{"client", "hds-t2.mint-tunnel.com", "", "hds-t2-mint-tunnel-com"},
		{"admin", "hds-t2.mint-tunnel.com", "", "admin-hds-t2"},
		{"admin", "", "", "admin"},
		{"server", "hds-t2.mint-tunnel.com", "", "hds-t2-mint-tunnel-com"},
		{"", "hds-t2.mint-tunnel.com", "", "hds-t2-mint-tunnel-com"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		if got := DefaultContextName(c.role, c.relay, c.user); got != c.want {
			t.Errorf("DefaultContextName(%q,%q,%q) = %q, want %q", c.role, c.relay, c.user, got, c.want)
		}
	}
}
