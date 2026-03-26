// Package grpc provides TLS certificate management for the gRPC server.
//
// EnsureCerts generates a self-signed CA and server certificate under
// <data-dir>/grpc/ on first startup. Subsequent calls are idempotent —
// if the files already exist they are loaded without regeneration.
//
// The generated PKI is intentionally simple:
//   - One root CA (ECDSA P-256, 10-year validity)
//   - One server certificate signed by that CA (SAN: DNS:arkeep-grpc)
//   - Per-agent client certificates issued on demand via IssueCertificate
//
// Agents verify the server using the CA cert and a fixed ServerName
// ("arkeep-grpc"), which decouples TLS verification from the server's
// actual hostname or IP address. This allows the same certificate to work
// regardless of how the server is addressed.
package grpc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const (
	// GRPCServerName is the fixed TLS ServerName used for the auto-generated
	// PKI. Agents must set tls.Config.ServerName to this value when connecting
	// with a CA cert issued by EnsureCerts.
	GRPCServerName = "arkeep-grpc"

	certValidity = 10 * 365 * 24 * time.Hour // ~10 years
)

// AutoCerts holds the in-memory representation of the auto-generated PKI.
// It is created once at startup by EnsureCerts and shared across the gRPC
// server and the enrollment HTTP handler.
type AutoCerts struct {
	// CACertFile is the on-disk path of the CA certificate (PEM).
	// Agents receive this file during enrollment.
	CACertFile string
	// CACertPEM is the raw PEM bytes of the CA certificate.
	// The enrollment handler returns this directly in the JSON response.
	CACertPEM []byte
	// ServerCertFile and ServerKeyFile are passed to the gRPC server's
	// tls.Config as the server identity certificate.
	ServerCertFile string
	ServerKeyFile  string
	// CAPool is a certificate pool containing only the generated CA.
	// Used by the gRPC server for RequireAndVerifyClientCert.
	CAPool *x509.CertPool

	// caKey and caCert are kept in memory to sign client certificates
	// without reading the key file on every enrollment request.
	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate

	// serialCounter is atomically incremented for each issued certificate
	// to guarantee unique serial numbers within a server lifetime.
	serialCounter atomic.Int64
}

// EnsureCerts loads the auto-generated PKI from <dataDir>/grpc/ if the files
// already exist, or generates a new CA + server certificate pair if they do not.
// Returns an error only if key generation or file I/O fails.
//
// The function is safe to call at startup before the gRPC listener opens.
func EnsureCerts(dataDir string, logger *zap.Logger) (*AutoCerts, error) {
	dir := filepath.Join(dataDir, "grpc")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("grpccerts: failed to create directory %s: %w", dir, err)
	}

	caCertFile := filepath.Join(dir, "ca.crt")
	caKeyFile := filepath.Join(dir, "ca.key")
	serverCertFile := filepath.Join(dir, "server.crt")
	serverKeyFile := filepath.Join(dir, "server.key")

	// If all four files exist, load and return without regenerating.
	if fileExists(caCertFile) && fileExists(caKeyFile) &&
		fileExists(serverCertFile) && fileExists(serverKeyFile) {
		logger.Info("loading existing gRPC PKI", zap.String("dir", dir))
		return loadCerts(caCertFile, caKeyFile, serverCertFile, serverKeyFile)
	}

	logger.Info("generating gRPC PKI (first startup)", zap.String("dir", dir))

	// ── Generate CA ──────────────────────────────────────────────────────────

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to generate CA key: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Arkeep gRPC CA", Organization: []string{"Arkeep"}},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(certValidity),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to parse CA certificate: %w", err)
	}

	// ── Generate server certificate ──────────────────────────────────────────

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to generate server key: %w", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: GRPCServerName, Organization: []string{"Arkeep"}},
		DNSNames:     []string{GRPCServerName},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     time.Now().Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to create server certificate: %w", err)
	}

	// ── Persist all four files atomically ────────────────────────────────────

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	caKeyPEM, err := ecKeyToPEM(caKey)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to encode CA key: %w", err)
	}

	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertDER})
	serverKeyPEM, err := ecKeyToPEM(serverKey)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to encode server key: %w", err)
	}

	if err := writeFileAtomic(caCertFile, caCertPEM, 0644); err != nil {
		return nil, fmt.Errorf("grpccerts: failed to write CA cert: %w", err)
	}
	if err := writeFileAtomic(caKeyFile, caKeyPEM, 0600); err != nil {
		return nil, fmt.Errorf("grpccerts: failed to write CA key: %w", err)
	}
	if err := writeFileAtomic(serverCertFile, serverCertPEM, 0644); err != nil {
		return nil, fmt.Errorf("grpccerts: failed to write server cert: %w", err)
	}
	if err := writeFileAtomic(serverKeyFile, serverKeyPEM, 0600); err != nil {
		return nil, fmt.Errorf("grpccerts: failed to write server key: %w", err)
	}

	logger.Info("gRPC PKI generated",
		zap.String("ca_cert", caCertFile),
		zap.String("server_cert", serverCertFile),
	)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	ac := &AutoCerts{
		CACertFile:     caCertFile,
		CACertPEM:      caCertPEM,
		ServerCertFile: serverCertFile,
		ServerKeyFile:  serverKeyFile,
		CAPool:         caPool,
		caKey:          caKey,
		caCert:         caCert,
	}
	ac.serialCounter.Store(2) // 1 = CA, 2 = server; next client starts at 3
	return ac, nil
}

// IssueCertificate signs a new client certificate with the given common name.
// Returns PEM-encoded certificate and private key bytes ready to be saved by
// the agent and loaded into a tls.Certificate.
func (ac *AutoCerts) IssueCertificate(cn string) (certPEM, keyPEM []byte, err error) {
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("grpccerts: failed to generate client key: %w", err)
	}

	serial := ac.serialCounter.Add(1)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"Arkeep"}},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     time.Now().Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ac.caCert, &clientKey.PublicKey, ac.caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("grpccerts: failed to sign client certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM, err = ecKeyToPEM(clientKey)
	if err != nil {
		return nil, nil, fmt.Errorf("grpccerts: failed to encode client key: %w", err)
	}

	return certPEM, keyPEM, nil
}

// TLSConfig returns a *tls.Config suitable for the gRPC server with mTLS enabled.
// It loads the server certificate from disk (so gRPC can reload it if needed)
// and configures the CA pool for client certificate verification.
func (ac *AutoCerts) TLSConfig() (*tls.Config, error) {
	serverCert, err := tls.LoadX509KeyPair(ac.ServerCertFile, ac.ServerKeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to load server key pair: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ac.CAPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// loadCerts reads existing PEM files from disk and reconstructs an AutoCerts.
func loadCerts(caCertFile, caKeyFile, serverCertFile, serverKeyFile string) (*AutoCerts, error) {
	caCertPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to read CA cert: %w", err)
	}
	caKeyPEM, err := os.ReadFile(caKeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to read CA key: %w", err)
	}

	// Parse CA certificate
	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return nil, fmt.Errorf("grpccerts: invalid CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to parse CA cert: %w", err)
	}

	// Parse CA private key
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("grpccerts: invalid CA key PEM")
	}
	caKeyRaw, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("grpccerts: failed to parse CA key: %w", err)
	}

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	ac := &AutoCerts{
		CACertFile:     caCertFile,
		CACertPEM:      caCertPEM,
		ServerCertFile: serverCertFile,
		ServerKeyFile:  serverKeyFile,
		CAPool:         caPool,
		caKey:          caKeyRaw,
		caCert:         caCert,
	}
	// Start serial counter well above 2 to avoid collisions with existing certs.
	ac.serialCounter.Store(1000)
	return ac, nil
}

// ecKeyToPEM marshals an ECDSA private key to SEC 1 / PEM format.
func ecKeyToPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// writeFileAtomic writes data to path using a temp file + rename to avoid
// partial writes visible to concurrent readers.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// fileExists returns true if path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
