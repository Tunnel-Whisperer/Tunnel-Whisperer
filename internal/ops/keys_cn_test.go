package ops

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
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

// writeIdentityFixture writes a CA (CN caCN) and a client cert issued from it
// (CN clientCN) into the config dir.
func writeIdentityFixture(t *testing.T, caCN, clientCN string) {
	t.Helper()
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	caPEM, caKeyPEM, err := pki.GenerateCA(caCN)
	if err != nil {
		t.Fatal(err)
	}
	clientPEM, clientKeyPEM, err := pki.IssueClientCert(caPEM, caKeyPEM, clientCN)
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
}

// TestEnsureCertsRefusesMismatchedStoredID: a stored server id that does not
// match the on-disk client cert CN must never trigger a silent CA
// regeneration — that would lock the admin out of its own relay.
func TestEnsureCertsRefusesMismatchedStoredID(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeIdentityFixture(t, "other-89abcdef", "other-89abcdef")
	caKeyBefore, err := os.ReadFile(config.CAKeyPath())
	if err != nil {
		t.Fatal(err)
	}

	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "server"
	o.cfg.Xray.RelayHost = "relay.example.com"
	o.cfg.Xray.UUID = "01234567-aaaa-bbbb-cccc-ddddeeeeffff"
	o.cfg.Xray.ServerID = "foo-01234567"

	err = o.ensureCerts()
	if err == nil || !strings.Contains(err.Error(), "does not match the stored server id") {
		t.Fatalf("ensureCerts error = %v, want CN/server-id mismatch refusal", err)
	}
	caKeyAfter, err := os.ReadFile(config.CAKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(caKeyBefore, caKeyAfter) {
		t.Error("ca.key was rewritten; the CA must never be regenerated on a stored-id mismatch")
	}
}

// TestEnsureCertsKeepsMatchingStoredID: CN == stored id is a no-op even when
// the hostname would derive a different id.
func TestEnsureCertsKeepsMatchingStoredID(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeIdentityFixture(t, "foo-01234567", "foo-01234567")
	caKeyBefore, err := os.ReadFile(config.CAKeyPath())
	if err != nil {
		t.Fatal(err)
	}

	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "server"
	o.cfg.Xray.RelayHost = "relay.example.com"
	o.cfg.Xray.UUID = "01234567-aaaa-bbbb-cccc-ddddeeeeffff"
	o.cfg.Xray.ServerID = "foo-01234567"

	if err := o.ensureCerts(); err != nil {
		t.Fatal(err)
	}
	caKeyAfter, err := os.ReadFile(config.CAKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(caKeyBefore, caKeyAfter) {
		t.Error("ca.key was rewritten for a matching stored id")
	}
	if got := readCertCN(t, config.ClientCertPath()); got != "foo-01234567" {
		t.Errorf("client cert CN = %q, want foo-01234567", got)
	}
}

// TestEnsureCertsRefusesUnreadableCertAfterDestroy: after `tw relay destroy`
// RelayHost is "" and an unreadable client.crt yields CN "" — the two must not
// compare equal and silently regenerate the CA.
func TestEnsureCertsRefusesUnreadableCertAfterDestroy(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeIdentityFixture(t, "foo-01234567", "foo-01234567")
	if err := os.WriteFile(config.ClientCertPath(), []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	caKeyBefore, err := os.ReadFile(config.CAKeyPath())
	if err != nil {
		t.Fatal(err)
	}

	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "server"
	o.cfg.Xray.RelayHost = ""
	o.cfg.Xray.UUID = "01234567-aaaa-bbbb-cccc-ddddeeeeffff"
	o.cfg.Xray.ServerID = "foo-01234567"

	err = o.ensureCerts()
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("ensureCerts error = %v, want unreadable-certificate refusal", err)
	}
	caKeyAfter, err := os.ReadFile(config.CAKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(caKeyBefore, caKeyAfter) {
		t.Error("ca.key was rewritten; the CA must never be regenerated for an unreadable client cert")
	}
}
