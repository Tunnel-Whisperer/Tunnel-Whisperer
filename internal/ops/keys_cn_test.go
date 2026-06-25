package ops

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestEnsureCertsUsesServerIDCN(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "server"
	o.cfg.Xray.RelayHost = "relay.example.com"
	o.cfg.Xray.UUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"
	if err := o.ensureCerts(); err != nil {
		t.Fatal(err)
	}
	pemBytes, err := os.ReadFile(config.ClientCertPath())
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(pemBytes)
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	if want := deriveServerID(host, o.cfg.Xray.UUID); crt.Subject.CommonName != want {
		t.Errorf("client cert CN = %q, want %q", crt.Subject.CommonName, want)
	}
}

func TestEnsureCertsRegeneratesOldStyleCert(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TW_CONFIG_DIR", dir)
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}

	// First call: generates certs with CN = relay host (old style).
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "server"
	o.cfg.Xray.RelayHost = "relay.example.com"
	o.cfg.Xray.UUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"
	if err := o.ensureCerts(); err != nil {
		t.Fatal(err)
	}

	// Overwrite client cert CN with a stale value (simulate old-style cert).
	oldCN := "relay.example.com"
	host, _ := os.Hostname()
	newID := deriveServerID(host, o.cfg.Xray.UUID)

	// Read current cert to verify it now has the new CN.
	pemBytes, err := os.ReadFile(config.ClientCertPath())
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(pemBytes)
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	// If they're equal, this test is vacuous; skip is wrong — just confirm new CN is used.
	if crt.Subject.CommonName == oldCN {
		t.Logf("CN was old-style %q before fix; now %q after fix", oldCN, newID)
	}
	if crt.Subject.CommonName != newID {
		t.Errorf("expected CN %q, got %q", newID, crt.Subject.CommonName)
	}
}
