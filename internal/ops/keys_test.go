package ops

import (
	"os"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestEnsureCertsCNNotTruncatedWhenUUIDEmpty is a regression test for the bug
// where ensureCerts ran before the Xray UUID was assigned, deriving a server-id
// of "<host>-" (empty first8) and baking a truncated CN into the CA/client cert
// while the relay config was later rendered with the full "<host>-<uuid8>".
func TestEnsureCertsCNNotTruncatedWhenUUIDEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)

	// admin install, no UUID yet — the exact state that produced "nwsl-".
	if err := os.WriteFile(config.FilePath(), []byte("mode: admin\n"), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	o, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	if err := o.ensureCerts(); err != nil {
		t.Fatalf("ensureCerts: %v", err)
	}

	uuid := o.Config().Xray.UUID
	if uuid == "" {
		t.Fatal("ensureCerts did not assign a UUID")
	}

	host, _ := os.Hostname()
	want := deriveServerID(host, uuid)
	if strings.HasSuffix(want, "-") {
		t.Fatalf("derived id still truncated: %q", want)
	}

	got := certCN(config.ClientCertPath())
	if got != want {
		t.Fatalf("client cert CN = %q, want %q", got, want)
	}
	if caCN := certCN(config.CACertPath()); caCN != want {
		t.Fatalf("CA cert CN = %q, want %q", caCN, want)
	}
}
