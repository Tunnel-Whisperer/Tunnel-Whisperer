package cli

import (
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestDaemonContextMismatch(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Mode = "server"
	cfg.Xray.RelayHost = "relay.example.com"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// Daemon matches the active context: no warning.
	if w := daemonContextMismatch("server", "relay.example.com"); w != "" {
		t.Errorf("expected no warning when in sync, got: %q", w)
	}

	// Mode differs (daemon still on the old admin context).
	w := daemonContextMismatch("admin", "relay.example.com")
	if !strings.Contains(w, "mode") {
		t.Errorf("expected mode mismatch warning, got: %q", w)
	}

	// Relay differs.
	w = daemonContextMismatch("server", "other.example.com")
	if !strings.Contains(w, "relay") {
		t.Errorf("expected relay mismatch warning, got: %q", w)
	}

	// Empty daemon fields are treated as unknown, not a mismatch.
	if w := daemonContextMismatch("", ""); w != "" {
		t.Errorf("expected no warning for empty daemon status, got: %q", w)
	}
}
