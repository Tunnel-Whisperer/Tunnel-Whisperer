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

func TestSignClientCSR(t *testing.T) {
	caCert, caKey, err := GenerateCA("srv-1")
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	keyPEM, csrPEM, err := GenerateKeyAndCSR("whatever-the-client-said")
	if err != nil {
		t.Fatalf("GenerateKeyAndCSR: %v", err)
	}
	certPEM, err := SignClientCSR(caCert, caKey, csrPEM, "srv-1")
	if err != nil {
		t.Fatalf("SignClientCSR: %v", err)
	}
	blk, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parsing issued cert: %v", err)
	}
	if cert.Subject.CommonName != "srv-1" {
		t.Fatalf("CN = %q, want the SIGNER-forced srv-1", cert.Subject.CommonName)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("ExtKeyUsage = %v", cert.ExtKeyUsage)
	}
	// The cert's public key must be the CSR key (the enrollee's), provably
	// pairable with keyPEM.
	kb, _ := pem.Decode(keyPEM)
	priv, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		t.Fatalf("parsing key: %v", err)
	}
	if !priv.PublicKey.Equal(cert.PublicKey) {
		t.Fatal("issued cert does not carry the CSR's public key")
	}
	// Garbage CSR is rejected.
	if _, err := SignClientCSR(caCert, caKey, []byte("not a csr"), "srv-1"); err == nil {
		t.Fatal("garbage CSR accepted")
	}
}

func TestSignClientCSRRejectsInvalidSignature(t *testing.T) {
	caCert, caKey, err := GenerateCA("srv-1")
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	_, csrPEM, err := GenerateKeyAndCSR("client")
	if err != nil {
		t.Fatalf("GenerateKeyAndCSR: %v", err)
	}

	// Decode and corrupt the CSR's signature bytes
	blk, _ := pem.Decode(csrPEM)
	if blk == nil {
		t.Fatal("csrPEM did not decode")
	}
	der := make([]byte, len(blk.Bytes))
	copy(der, blk.Bytes)

	// Flip a bit in the signature portion (near the end of the DER)
	if len(der) > 10 {
		der[len(der)-5] ^= 0x01
	}

	// Re-encode to PEM
	tamperedCSRPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})

	// SignClientCSR must reject the tampered signature
	_, err = SignClientCSR(caCert, caKey, tamperedCSRPEM, "srv-1")
	if err == nil {
		t.Fatal("tampered CSR accepted; signature verification failed")
	}
}
