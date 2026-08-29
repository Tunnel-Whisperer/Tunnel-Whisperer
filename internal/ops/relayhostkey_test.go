package ops

import (
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestEnsureRelayHostKeyIsStableAndPinnable(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}

	// Before any provision, nothing is pinned.
	if pk, err := loadPinnedRelayHostKey(); err != nil || pk != nil {
		t.Fatalf("expected no pinned key initially, got %v, %v", pk, err)
	}

	priv1, pub1, err := ensureRelayHostKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(priv1) == 0 || pub1 == "" {
		t.Fatal("ensureRelayHostKey returned empty material")
	}

	// Idempotent: a second call reuses the same key so the pin stays stable
	// across reprovisions.
	priv2, pub2, err := ensureRelayHostKey()
	if err != nil {
		t.Fatal(err)
	}
	if string(priv1) != string(priv2) || pub1 != pub2 {
		t.Fatal("ensureRelayHostKey must reuse the persisted key, not regenerate it")
	}

	// The pinned public key now loads and matches what we injected.
	pinned, err := loadPinnedRelayHostKey()
	if err != nil || pinned == nil {
		t.Fatalf("expected a pinned key after ensure, got %v, %v", pinned, err)
	}
	if pinned.Type() != "ssh-ed25519" {
		t.Errorf("pinned host key type = %q, want ssh-ed25519", pinned.Type())
	}
}
