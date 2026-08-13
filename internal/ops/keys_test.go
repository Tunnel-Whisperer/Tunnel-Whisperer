package ops

import (
	"os"
	"path/filepath"
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

// A user-set relative cert path in config.yaml must be absolutized against
// the config dir before it reaches xray-core (which resolves relative paths
// against its own executable directory).
func TestApplyClientCertPathsAbsolutizesRelative(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	xc := &config.XrayConfig{ClientCertPath: "certs/c.crt", ClientKeyPath: "certs/c.key"}
	applyClientCertPaths(xc)
	if !filepath.IsAbs(xc.ClientCertPath) || !filepath.IsAbs(xc.ClientKeyPath) {
		t.Fatalf("paths not absolutized: %q %q", xc.ClientCertPath, xc.ClientKeyPath)
	}
	if want := filepath.Join(config.Dir(), "certs", "c.crt"); xc.ClientCertPath != want {
		t.Fatalf("cert = %q, want %q", xc.ClientCertPath, want)
	}
}
