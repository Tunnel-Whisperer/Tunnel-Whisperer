package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
	"gopkg.in/yaml.v3"
)

func TestModeError(t *testing.T) {
	// Unset mode is always allowed (setup not done yet).
	if err := modeError("", []string{"server"}); err != nil {
		t.Errorf("modeError(\"\", [server]) = %v, want nil", err)
	}
	// Current mode present in the allow-list is permitted.
	if err := modeError("relay", []string{"relay", "server"}); err != nil {
		t.Errorf("modeError(relay, [relay server]) = %v, want nil", err)
	}
	if err := modeError("server", []string{"server"}); err != nil {
		t.Errorf("modeError(server, [server]) = %v, want nil", err)
	}
	// Current mode absent from the allow-list is rejected.
	if err := modeError("client", []string{"server"}); err == nil {
		t.Error("modeError(client, [server]) = nil, want error")
	}
	if err := modeError("server", []string{"client", "relay"}); err == nil {
		t.Error("modeError(server, [client relay]) = nil, want error")
	}
	// Relay ownership moved to relay mode: server mode must be refused the
	// relay-gated relay commands (e.g. `tw relay create`), and relay allowed.
	if err := modeError("server", []string{"relay"}); err == nil {
		t.Error("modeError(server, [relay]) = nil, want error (server must not run `tw relay create`)")
	}
	if err := modeError("relay", []string{"relay"}); err != nil {
		t.Errorf("modeError(relay, [relay]) = %v, want nil", err)
	}
}

func writeCLIProfileKey(t *testing.T) {
	t.Helper()
	priv, pub, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(config.Dir(), "id_ed25519"), priv, 0o600)
	os.WriteFile(filepath.Join(config.Dir(), "id_ed25519.pub"), pub, 0o644)
}

func readCLIIdentity(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func readCLIPriv(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeCLIConfig(t *testing.T, mode, sig, issuer string) {
	t.Helper()
	cfg := &config.Config{Mode: mode}
	if sig != "" {
		cfg.ModeAuth = &config.ModeAuth{Sig: sig, Issuer: issuer}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.FilePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRequireModeRejectsTamperedSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	// A profile that claims mode=server with a signature that covers a
	// DIFFERENT identity/mode must be refused.
	writeCLIProfileKey(t)
	id := readCLIIdentity(t)
	// Sign for "client", then claim "server" on disk.
	priv := readCLIPriv(t)
	sig, issuer, _ := modeauth.Sign(priv, "client", id)
	writeCLIConfig(t, "server", sig, issuer)
	if err := requireMode("server"); err == nil {
		t.Error("requireMode accepted a signature that does not cover mode=server")
	}
}

func TestRequireModeAllowsValidSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeCLIProfileKey(t)
	id := readCLIIdentity(t)
	priv := readCLIPriv(t)
	sig, issuer, _ := modeauth.Sign(priv, "server", id)
	writeCLIConfig(t, "server", sig, issuer)
	if err := requireMode("server"); err != nil {
		t.Errorf("requireMode rejected a valid signature: %v", err)
	}
}

func TestRequireModeAllowsUnsignedLegacy(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeCLIProfileKey(t)
	writeCLIConfig(t, "server", "", "") // no mode_auth
	if err := requireMode("server"); err != nil {
		t.Errorf("legacy unsigned profile should be tolerated: %v", err)
	}
}
