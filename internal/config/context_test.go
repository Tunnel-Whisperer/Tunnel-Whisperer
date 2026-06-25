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
	if idx.CurrentContext != "default" {
		t.Fatalf("current = %q, want default", idx.CurrentContext)
	}
	m, ok := idx.Contexts["default"]
	if !ok || m.Role != "admin" || m.Relay != "a.example.com" {
		t.Fatalf("default meta wrong: %+v", idx.Contexts)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "contexts.yaml")); err != nil {
		t.Errorf("index not written: %v", err)
	}
}
