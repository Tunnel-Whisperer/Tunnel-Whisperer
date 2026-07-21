package ops

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
)

func TestGetUserConfigBundleProducesClientContext(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)

	// Per-server cert/key (presented to the relay mTLS gate) live in the config dir.
	writeFile(t, config.ClientCertPath(), "CERT")
	writeFile(t, config.ClientKeyPath(), "KEY")

	// The user's profile: a client config.yaml (no mode) + SSH identity.
	userDir := filepath.Join(config.UsersDir(), "alice")
	writeFile(t, filepath.Join(userDir, "config.yaml"), "xray:\n  relay_host: relay.example.com\n  relay_port: 443\n")
	writeFile(t, filepath.Join(userDir, "id_ed25519"), "SSHKEY")
	writeFile(t, filepath.Join(userDir, "id_ed25519.pub"), "ssh-ed25519 AAAA")

	bundle, err := o.GetUserConfigBundle("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bundle, []byte("TWBOX1")) {
		t.Fatal("bundle is not a sealed cryptobox blob")
	}
	// User bundles carry no passphrase — the bundle opens with "".
	plain, err := cryptobox.Decrypt(bundle, "")
	if err != nil {
		t.Fatalf("decrypt user bundle with empty passphrase: %v", err)
	}

	// Indexes as a client context with the user's relay.
	bm := readBundleMeta(plain)
	if bm.Role != "client" {
		t.Errorf("role = %q, want client", bm.Role)
	}
	if bm.Relay != "relay.example.com" {
		t.Errorf("relay = %q, want relay.example.com", bm.Relay)
	}

	// The full client profile is present.
	for _, name := range []string{"config.yaml", "client.crt", "client.key", "id_ed25519", "id_ed25519.pub"} {
		if _, err := readZipEntry(plain, name); err != nil {
			t.Errorf("missing profile entry %q: %v", name, err)
		}
	}

	// Wrong passphrase must not open it.
	if _, err := cryptobox.Decrypt(bundle, "wrong-passphrase"); err == nil {
		t.Error("expected decrypt failure with wrong passphrase")
	}
}

func TestGetUserConfigBundleUnknownUser(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newOpsForTest(t)
	if _, err := o.GetUserConfigBundle("nobody"); err == nil {
		t.Error("expected error for unknown user")
	}
}
