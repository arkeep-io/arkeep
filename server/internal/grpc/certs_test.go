package grpc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"

	"go.uber.org/zap"
)

func newCSR(t *testing.T, key any, cn string) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

// TestSignCSR covers enrollment with an agent-generated key (SEC-26): the
// server signs the CSR's public key under a CN of its own choosing, and the
// private key never leaves the agent.
func TestSignCSR(t *testing.T) {
	ac, err := EnsureCerts(t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	certPEM, err := ac.SignCSR(newCSR(t, key, "chosen-by-agent"), "arkeep-agent-x")
	if err != nil {
		t.Fatalf("SignCSR: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("SignCSR returned no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		t.Error("the certificate does not carry the CSR's public key")
	}
	if cert.Subject.CommonName != "arkeep-agent-x" {
		t.Errorf("CN = %q, want the server-chosen %q", cert.Subject.CommonName, "arkeep-agent-x")
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ac.CACertPEM)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Errorf("the certificate does not verify as a client certificate of the CA: %v", err)
	}

	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("GenerateKey(rsa): %v", err)
	}
	tampered := newCSR(t, key, "x")
	tampered[len(tampered)/2] ^= 0x01

	for name, csr := range map[string][]byte{
		"not PEM":      []byte("garbage"),
		"wrong block":  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}),
		"bad DER":      pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte{1, 2, 3}}),
		"tampered":     tampered,
		"weak RSA key": newCSR(t, weak, "x"),
	} {
		if _, err := ac.SignCSR(csr, "arkeep-agent-x"); err == nil {
			t.Errorf("SignCSR(%s) succeeded, want an error", name)
		}
	}
}
