package ops

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/pki"
)

func readCertCN(t *testing.T, path string) string {
	t.Helper()
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		t.Fatalf("no PEM block in %s", path)
	}
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return crt.Subject.CommonName
}

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

	const oldCN = "relay.example.com"

	// Manually write an OLD-STYLE identity to disk: CA + client cert whose CN
	// is the relay host (the pre-phase-2 derivation). ensureCerts must detect
	// the stale CN and re-issue against the new server-id.
	caPEM, caKeyPEM, err := pki.GenerateCA(oldCN)
	if err != nil {
		t.Fatal(err)
	}
	clientPEM, clientKeyPEM, err := pki.IssueClientCert(caPEM, caKeyPEM, oldCN)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{config.CACertPath(), caPEM, 0o644},
		{config.CAKeyPath(), caKeyPEM, 0o600},
		{config.ClientCertPath(), clientPEM, 0o644},
		{config.ClientKeyPath(), clientKeyPEM, 0o600},
	} {
		if err := os.WriteFile(w.path, w.data, w.mode); err != nil {
			t.Fatal(err)
		}
	}

	// Sanity: the on-disk client cert starts with the stale CN.
	if got := readCertCN(t, config.ClientCertPath()); got != oldCN {
		t.Fatalf("precondition: client CN = %q, want %q", got, oldCN)
	}

	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "server"
	o.cfg.Xray.RelayHost = oldCN
	o.cfg.Xray.UUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"
	if err := o.ensureCerts(); err != nil {
		t.Fatal(err)
	}

	host, _ := os.Hostname()
	want := deriveServerID(host, o.cfg.Xray.UUID)
	got := readCertCN(t, config.ClientCertPath())
	if got == oldCN {
		t.Errorf("client cert CN still stale %q; expected regeneration to %q", oldCN, want)
	}
	if got != want {
		t.Errorf("client cert CN = %q, want %q", got, want)
	}
}
