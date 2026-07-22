package config

import (
	"os"
	"path/filepath"
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
