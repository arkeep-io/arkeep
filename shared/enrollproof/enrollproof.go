// Package enrollproof authenticates agent enrollment with the shared agent
// secret without sending it (ADR 0023). Enrollment may run over plain HTTP,
// so neither side can trust the channel:
//
//   - the agent proves it knows the secret with an HMAC over its CSR, instead
//     of sending the secret itself;
//   - the server proves it knows the secret with an HMAC over the agent's CSR
//     and the certificates it returns, or over the CSR alone when it answers
//     that it has no auto-PKI (405).
//
// Someone on the network path learns no secret and cannot hand the agent a CA
// of their own, nor make it give up its client certificate. Every proof is
// bound to the CSR of the request, so a proof cannot be reused for another one.
package enrollproof

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// Each proof has its own label, so that one can never stand for another.
const (
	requestLabel     = "arkeep-enroll-request-v1"
	responseLabel    = "arkeep-enroll-response-v1"
	unsupportedLabel = "arkeep-enroll-unsupported-v1"
)

// Request is the agent's proof for an enrollment request carrying csrPEM.
func Request(secret, csrPEM string) string {
	return mac(secret, requestLabel, csrPEM)
}

// Response is the server's proof for a successful enrollment: the CA and the
// client certificate it returns for csrPEM.
func Response(secret, csrPEM, caCertPEM, clientCertPEM string) string {
	return mac(secret, responseLabel, csrPEM, caCertPEM, clientCertPEM)
}

// Unsupported is the server's proof that it has no auto-PKI and issues no
// client certificate for csrPEM.
func Unsupported(secret, csrPEM string) string {
	return mac(secret, unsupportedLabel, csrPEM)
}

// Equal reports, in constant time, whether proof is the expected one.
func Equal(expected, proof string) bool {
	e, err1 := hex.DecodeString(expected)
	p, err2 := hex.DecodeString(proof)
	return err1 == nil && err2 == nil && len(p) == sha256.Size && hmac.Equal(e, p)
}

// mac is the hex HMAC-SHA256 of label and parts under secret. Each part is
// length-prefixed, so that moving bytes from one part to the next changes the
// result.
func mac(secret, label string, parts ...string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(label))
	var n [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
