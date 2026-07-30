package ops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	"gopkg.in/yaml.v3"
)

func TestUserBundleCarriesClientModeSignature(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeProfileKey(t) // the SERVER's own key
	o := newOpsForTest(t)

	// Per-server cert/key (presented to the relay mTLS gate) live in the config dir.
	writeFile(t, config.ClientCertPath(), "CERT")
	writeFile(t, config.ClientKeyPath(), "KEY")

	// A user, laid out the same way CreateUser would (see userexport_test.go):
	// a client config.yaml + SSH identity, so GetUserConfigBundle has something
	// to export.
	userDir := filepath.Join(config.UsersDir(), "alice")
	writeFile(t, filepath.Join(userDir, "config.yaml"), "xray:\n  relay_host: relay.example.com\n  relay_port: 443\n")
	writeFile(t, filepath.Join(userDir, "id_ed25519"), "SSHKEY")
	writeFile(t, filepath.Join(userDir, "id_ed25519.pub"), "ssh-ed25519 AAAA alice")

	bundle, err := o.GetUserConfigBundle("alice")
	if err != nil {
		t.Fatal(err)
	}
	// Unseal (empty passphrase) → unzip → read config.yaml.
	plain, err := cryptobox.Decrypt(bundle, "")
	if err != nil {
		t.Fatal(err)
	}
	cfgYAML, err := readZipEntry(plain, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	userPub, err := readZipEntry(plain, "id_ed25519.pub")
	if err != nil {
		t.Fatal(err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(cfgYAML, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "client" || cfg.ModeAuth == nil {
		t.Fatalf("bundle config missing client mode_auth: mode=%q auth=%v", cfg.Mode, cfg.ModeAuth)
	}
	if err := modeauth.Verify("client", strings.TrimSpace(string(userPub)), cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err != nil {
		t.Errorf("client mode signature does not verify: %v", err)
	}
}
