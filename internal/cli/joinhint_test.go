package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJoinCommandHint(t *testing.T) {
	h := joinCommandHint("relay.example.com", "ABCD-1234")
	for _, want := range []string{
		"  sudo tw join relay.example.com ABCD-1234\n",
		"(Windows: run as Administrator, without sudo. Neither is needed when TW_CONFIG_DIR points to a directory you own.)",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("hint missing %q:\n%s", want, h)
		}
	}
}

// An unwritable config dir is detected before any relay contact, so the
// error must say the code is still unused and spell out the elevated retry.
func TestJoinPreflightNamesRetry(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TW_CONFIG_DIR", filepath.Join(blocker, "config"))

	err := joinPreflight("relay.example.com", "ABCD-1234")
	if err == nil {
		t.Fatal("expected an error for an unwritable config dir")
	}
	for _, want := range []string{
		"is not writable",
		"still unused",
		"nothing was sent to the relay",
		"sudo tw join relay.example.com ABCD-1234",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}
