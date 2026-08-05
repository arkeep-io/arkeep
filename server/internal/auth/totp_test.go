package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestNewTOTPKey(t *testing.T) {
	secret, url, err := NewTOTPKey("alice@example.com")
	if err != nil {
		t.Fatalf("NewTOTPKey: %v", err)
	}
	if secret == "" {
		t.Error("secret is empty")
	}
	if !strings.HasPrefix(url, "otpauth://totp/") {
		t.Errorf("url = %q, want otpauth://totp/ prefix", url)
	}
	if !strings.Contains(url, "issuer=Arkeep") {
		t.Errorf("url = %q, want issuer=Arkeep", url)
	}
	if !strings.Contains(url, "secret="+secret) {
		t.Errorf("url %q does not carry the returned secret", url)
	}
}

func TestValidateTOTPCode(t *testing.T) {
	secret, _, err := NewTOTPKey("alice@example.com")
	if err != nil {
		t.Fatalf("NewTOTPKey: %v", err)
	}

	now := time.Now()

	codeNow, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	// One period earlier and later — accepted thanks to the +/-1 skew window.
	codePrev, err := totp.GenerateCode(secret, now.Add(-30*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode prev: %v", err)
	}
	codeNext, err := totp.GenerateCode(secret, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode next: %v", err)
	}
	// Far outside the window.
	codeStale, err := totp.GenerateCode(secret, now.Add(-10*time.Minute))
	if err != nil {
		t.Fatalf("GenerateCode stale: %v", err)
	}

	tests := []struct {
		name   string
		secret string
		code   string
		want   bool
	}{
		{"current period", secret, codeNow, true},
		{"one period behind", secret, codePrev, true},
		{"one period ahead", secret, codeNext, true},
		{"far in the past", secret, codeStale, false},
		{"wrong code", secret, "000000", false},
		{"empty code", secret, "", false},
		{"empty secret", "", codeNow, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateTOTPCode(tt.secret, tt.code); got != tt.want {
				t.Errorf("ValidateTOTPCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("len = %d, want %d", len(codes), RecoveryCodeCount)
	}

	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if len(c) != 19 { // 16 base32 chars + 3 dashes
			t.Errorf("code %q has length %d, want 19", c, len(c))
		}
		if strings.Count(c, "-") != 3 {
			t.Errorf("code %q should have 3 dashes", c)
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestNormaliseRecoveryCode(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ABCD-EFGH-IJKL-MNOP", "ABCDEFGHIJKLMNOP"},
		{"abcd-efgh-ijkl-mnop", "ABCDEFGHIJKLMNOP"},
		{"  ABCDEFGHIJKLMNOP  ", "ABCDEFGHIJKLMNOP"},
	}
	for _, tt := range tests {
		if got := NormaliseRecoveryCode(tt.in); got != tt.want {
			t.Errorf("NormaliseRecoveryCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
