package enrollproof

import "testing"

func TestProofs(t *testing.T) {
	const secret, csr, ca, cert = "secret", "csr-pem", "ca-pem", "cert-pem"

	if !Equal(Request(secret, csr), Request(secret, csr)) {
		t.Fatal("a proof does not equal itself")
	}
	tests := []struct {
		name     string
		expected string
		got      string
	}{
		{"another secret", Request(secret, csr), Request("other", csr)},
		{"another CSR", Request(secret, csr), Request(secret, "other-csr")},
		{"request used as unsupported", Unsupported(secret, csr), Request(secret, csr)},
		{"another CA", Response(secret, csr, ca, cert), Response(secret, csr, "other-ca", cert)},
		// Length prefixes: the same bytes split differently are another proof.
		{"bytes moved between parts", Response(secret, csr, ca, cert), Response(secret, csr, "ca-pemcert", "-pem")},
		{"not hex", Request(secret, csr), "zz"},
		{"empty", Request(secret, csr), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if Equal(tt.expected, tt.got) {
				t.Error("Equal = true, want false")
			}
		})
	}
}
