package pki

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestGenerateCAProducesValidCA(t *testing.T) {
	certPEM, keyPEM, err := GenerateCA("test-server")
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("CA cert PEM did not decode")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing CA cert: %v", err)
	}
	if !cert.IsCA {
		t.Error("CA cert IsCA = false, want true")
	}
	if cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Errorf("CA MaxPathLen=%d MaxPathLenZero=%v, want 0/true", cert.MaxPathLen, cert.MaxPathLenZero)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("CA key PEM did not decode")
	}
	if _, err := x509.ParseECPrivateKey(keyBlock.Bytes); err != nil {
		t.Errorf("CA key PEM did not parse: %v", err)
	}
}

func TestIssuedClientCertVerifiesAgainstCA(t *testing.T) {
	caCertPEM, caKeyPEM, err := GenerateCA("test-server")
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	clientCertPEM, _, err := IssueClientCert(caCertPEM, caKeyPEM, "test-server")
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCertPEM) {
		t.Fatal("failed to add CA to pool")
	}
	block, _ := pem.Decode(clientCertPEM)
	clientCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing client cert: %v", err)
	}
	if _, err := clientCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("client cert failed to verify against its CA: %v", err)
	}
}

func TestClientCertRejectedByDifferentCA(t *testing.T) {
	caCertPEM, caKeyPEM, err := GenerateCA("server-a")
	if err != nil {
		t.Fatalf("GenerateCA(server-a): %v", err)
	}
	otherCAPEM, _, err := GenerateCA("server-b")
	if err != nil {
		t.Fatalf("GenerateCA(server-b): %v", err)
	}
	clientCertPEM, _, err := IssueClientCert(caCertPEM, caKeyPEM, "server-a")
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(otherCAPEM) {
		t.Fatal("failed to add other CA to pool")
	}
	block, _ := pem.Decode(clientCertPEM)
	if block == nil {
		t.Fatal("client cert PEM did not decode")
	}
	clientCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing client cert: %v", err)
	}
	if _, err := clientCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err == nil {
		t.Error("client cert verified against the wrong CA; expected failure")
	}
}
