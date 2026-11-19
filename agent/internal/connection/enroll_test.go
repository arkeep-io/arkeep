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
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/shared/enrollproof"
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

// testSecret is the agent secret of the fake servers below.
const testSecret = "secret"

// readCSR decodes an enrollment request, failing the request unless it
// carries a valid proof of testSecret and no secret.
func readCSR(t *testing.T, w http.ResponseWriter, r *http.Request) (*x509.CertificateRequest, string, bool) {
	t.Helper()
	var body struct {
		AgentSecret string `json:"agent_secret"`
		CSR         string `json:"csr"`
		Proof       string `json:"proof"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return nil, "", false
	}
	if body.AgentSecret != "" {
		t.Errorf("the agent sent the secret itself: %q", body.AgentSecret)
	}
	if !enrollproof.Equal(enrollproof.Request(testSecret, body.CSR), body.Proof) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", false
	}
	block, _ := pem.Decode([]byte(body.CSR))
	if block == nil {
		http.Error(w, "no csr", http.StatusBadRequest)
		return nil, "", false
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		http.Error(w, "invalid csr", http.StatusBadRequest)
		return nil, "", false
	}
	return csr, body.CSR, true
}

// provenResponse is a successful answer signed with secret.
func provenResponse(secret, csrPEM, caPEM, certPEM string) map[string]string {
	return map[string]string{
		"ca_cert":     caPEM,
		"client_cert": certPEM,
		"proof":       enrollproof.Response(secret, csrPEM, caPEM, certPEM),
	}
}

func enrollAgainst(t *testing.T, handler http.HandlerFunc) (*Manager, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	m := &Manager{
		cfg:    Config{ServerHTTPAddr: srv.URL, SharedSecret: testSecret, StateDir: t.TempDir()},
		logger: zap.NewNop(),
	}
	return m, m.Enroll(context.Background())
}

// TestEnroll_SendsCSRAndKeepsKey: the agent sends a CSR with a proof of the
// secret and not the secret, the server returns only the certificate, and the
// key the agent stores is the one it generated.
func TestEnroll_SendsCSRAndKeepsKey(t *testing.T) {
	ca := newTestCA(t)
	m, err := enrollAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		csr, csrPEM, ok := readCSR(t, w, r)
		if !ok {
			return
		}
		_ = json.NewEncoder(w).Encode(provenResponse(testSecret, csrPEM, string(ca.pem), string(ca.sign(t, csr.PublicKey))))
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(m.cfg.ClientCertFile, m.cfg.ClientKeyFile); err != nil {
		t.Errorf("stored certificate and key do not form a pair: %v", err)
	}
}

// TestEnroll_RefusesUnprovenAnswers covers S-04: an answer without the
// server's proof of the secret is never trusted, whether it hands over a CA
// (someone on the path, or a server older than the agent) or claims the server
// has no auto-PKI.
func TestEnroll_RefusesUnprovenAnswers(t *testing.T) {
	ca := newTestCA(t)
	tests := []struct {
		name   string
		answer func(w http.ResponseWriter, csr *x509.CertificateRequest, csrPEM string)
	}{
		{"certificate without proof", func(w http.ResponseWriter, csr *x509.CertificateRequest, _ string) {
			_ = json.NewEncoder(w).Encode(map[string]string{"ca_cert": string(ca.pem), "client_cert": string(ca.sign(t, csr.PublicKey))})
		}},
		{"certificate proven with another secret", func(w http.ResponseWriter, csr *x509.CertificateRequest, csrPEM string) {
			_ = json.NewEncoder(w).Encode(provenResponse("other", csrPEM, string(ca.pem), string(ca.sign(t, csr.PublicKey))))
		}},
		{"proof for another CA", func(w http.ResponseWriter, csr *x509.CertificateRequest, csrPEM string) {
			cert := string(ca.sign(t, csr.PublicKey))
			resp := provenResponse(testSecret, csrPEM, "another CA", cert)
			resp["ca_cert"] = string(ca.pem)
			_ = json.NewEncoder(w).Encode(resp)
		}},
		{"405 without proof", func(w http.ResponseWriter, _ *x509.CertificateRequest, _ string) {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}},
		{"405 with a success proof", func(w http.ResponseWriter, _ *x509.CertificateRequest, csrPEM string) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"proof": enrollproof.Request(testSecret, csrPEM)})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := enrollAgainst(t, func(w http.ResponseWriter, r *http.Request) {
				csr, csrPEM, ok := readCSR(t, w, r)
				if ok {
					tt.answer(w, csr, csrPEM)
				}
			})
			if !errors.Is(err, errUnprovenAnswer) {
				t.Fatalf("Enroll = %v, want errUnprovenAnswer", err)
			}
			if m.cfg.ClientCertFile != "" || m.cfg.TLSCAFile != "" {
				t.Error("an unproven answer was configured")
			}
		})
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
	m, err := enrollAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		_, csrPEM, ok := readCSR(t, w, r)
		if !ok {
			return
		}
		_ = json.NewEncoder(w).Encode(provenResponse(testSecret, csrPEM, string(ca.pem), string(ca.sign(t, &other.PublicKey))))
	})
	if err == nil {
		t.Fatal("Enroll accepted a certificate for another key")
	}
	if m.cfg.ClientCertFile != "" {
		t.Error("the mismatched certificate was configured")
	}
}

// TestEnsureEnrolled_RetriesUntilEnrolled: an enrollment that fails because the
// server is not answering yet is tried again on the next connection attempt.
func TestEnsureEnrolled_RetriesUntilEnrolled(t *testing.T) {
	ca := newTestCA(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		csr, csrPEM, ok := readCSR(t, w, r)
		if !ok {
			return
		}
		_ = json.NewEncoder(w).Encode(provenResponse(testSecret, csrPEM, string(ca.pem), string(ca.sign(t, csr.PublicKey))))
	}))
	t.Cleanup(srv.Close)
	m := &Manager{
		cfg:    Config{ServerHTTPAddr: srv.URL, SharedSecret: testSecret, StateDir: t.TempDir()},
		logger: zap.NewNop(),
	}

	m.ensureEnrolled(context.Background())
	if m.cfg.ClientCertFile != "" {
		t.Fatal("enrolled although the server refused the first request")
	}
	m.ensureEnrolled(context.Background())
	if m.cfg.ClientCertFile == "" {
		t.Fatal("the second attempt did not enroll")
	}
	m.ensureEnrolled(context.Background())
	if calls != 2 {
		t.Errorf("enrollment requests = %d, want 2: an enrolled agent must not enroll again", calls)
	}
}

// TestEnsureEnrolled_StopsWhenUnsupported: a server without auto-PKI answers
// 405 with its proof, and the agent stops asking.
func TestEnsureEnrolled_StopsWhenUnsupported(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if _, csrPEM, ok := readCSR(t, w, r); ok {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"proof": enrollproof.Unsupported(testSecret, csrPEM)})
		}
	}))
	t.Cleanup(srv.Close)
	m := &Manager{
		cfg:    Config{ServerHTTPAddr: srv.URL, SharedSecret: testSecret, StateDir: t.TempDir()},
		logger: zap.NewNop(),
	}
	m.ensureEnrolled(context.Background())
	m.ensureEnrolled(context.Background())
	if calls != 1 {
		t.Errorf("enrollment requests = %d, want 1", calls)
	}
}
