package connection

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

// testCA signs client certificates the way the server's auto-PKI does.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate(CA): %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca *testCA) sign(t *testing.T, pub any) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "agent"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func enrollAgainst(t *testing.T, handler http.HandlerFunc) (*Manager, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	m := &Manager{
		cfg:    Config{ServerHTTPAddr: srv.URL, SharedSecret: "secret", StateDir: t.TempDir()},
		logger: zap.NewNop(),
	}
	return m, m.Enroll(context.Background())
}

// TestEnroll_SendsCSRAndKeepsKey: the agent sends a CSR, the server returns
// only the certificate, and the key the agent stores is the one it generated.
func TestEnroll_SendsCSRAndKeepsKey(t *testing.T) {
	ca := newTestCA(t)
	m, err := enrollAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AgentSecret string `json:"agent_secret"`
			CSR         string `json:"csr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AgentSecret != "secret" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		block, _ := pem.Decode([]byte(body.CSR))
		if block == nil {
			http.Error(w, "no csr", http.StatusBadRequest)
			return
		}
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil || csr.CheckSignature() != nil {
			http.Error(w, "invalid csr", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"ca_cert":     string(ca.pem),
			"client_cert": string(ca.sign(t, csr.PublicKey)),
		})
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(m.cfg.ClientCertFile, m.cfg.ClientKeyFile); err != nil {
		t.Errorf("stored certificate and key do not form a pair: %v", err)
	}
}

// TestEnroll_OldServerGeneratedKey keeps agents working with a server that
// ignores the CSR and returns a key of its own.
func TestEnroll_OldServerGeneratedKey(t *testing.T) {
	ca := newTestCA(t)
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(serverKey)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	m, err := enrollAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"ca_cert":     string(ca.pem),
			"client_cert": string(ca.sign(t, &serverKey.PublicKey)),
			"client_key":  string(keyPEM),
		})
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	stored, err := os.ReadFile(m.cfg.ClientKeyFile)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if string(stored) != string(keyPEM) {
		t.Error("the stored key is not the one the old server sent")
	}
}

// TestEnroll_RejectsMismatchedCertificate: a certificate for another key is
// never stored.
func TestEnroll_RejectsMismatchedCertificate(t *testing.T) {
	ca := newTestCA(t)
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	m, err := enrollAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"ca_cert":     string(ca.pem),
			"client_cert": string(ca.sign(t, &other.PublicKey)),
		})
	})
	if err == nil {
		t.Fatal("Enroll accepted a certificate for another key")
	}
	if m.cfg.ClientCertFile != "" {
		t.Error("the mismatched certificate was configured")
	}
}

func TestIsPlainHTTPRemote(t *testing.T) {
	for url, want := range map[string]bool{
		"http://arkeep.example.com:8080": true,
		"http://192.168.1.10:8080":       true,
		"https://arkeep.example.com":     false,
		"http://localhost:8080":          false,
		"http://127.0.0.1:8080":          false,
		"http://[::1]:8080":              false,
	} {
		if got := isPlainHTTPRemote(url); got != want {
			t.Errorf("isPlainHTTPRemote(%q) = %v, want %v", url, got, want)
		}
	}
}
