package auth

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/pquerna/otp/totp"
)

const (
	// totpIssuer is the label authenticator apps show next to the account.
	totpIssuer = "Arkeep"

	// totpSecretBytes is the shared secret length. RFC 4226 recommends at
	// least 128 bits; 160 bits matches what authenticator apps expect.
	totpSecretBytes = 20

	// RecoveryCodeCount is how many single-use recovery codes are issued when
	// a user enables two-factor authentication.
	RecoveryCodeCount = 10

	// recoveryCodeBytes gives 80 bits of entropy per code. This is what makes
	// storing them under an unsalted SHA-256 (HashToken) safe — short or
	// human-chosen codes would need a KDF instead.
	recoveryCodeBytes = 10
)

// recoveryCodeEncoding is unpadded base32, so 10 bytes render as exactly 16
// characters, formatted as four dash-separated groups for readability.
var recoveryCodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPKey creates a fresh TOTP shared secret for the given account and
// returns the base32 secret together with the otpauth:// URL the GUI renders as
// a QR code. The secret must be stored encrypted (db.EncryptedString).
func NewTOTPKey(email string) (secret, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: email,
		SecretSize:  totpSecretBytes,
	})
	if err != nil {
		return "", "", fmt.Errorf("auth: generating totp key: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTPCode reports whether code is a valid six-digit TOTP value for
// secret. totp.Validate applies the Google Authenticator defaults — 30 second
// period, SHA-1, six digits — and a skew of one period either side, which is
// the clock-drift tolerance we want.
func ValidateTOTPCode(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}
	return totp.Validate(code, secret)
}

// GenerateRecoveryCodes returns n formatted single-use recovery codes. Only the
// SHA-256 hash of NormaliseRecoveryCode(code) is ever persisted.
func GenerateRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		b := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("auth: generating recovery code: %w", err)
		}
		raw := recoveryCodeEncoding.EncodeToString(b)
		codes = append(codes, fmt.Sprintf("%s-%s-%s-%s", raw[0:4], raw[4:8], raw[8:12], raw[12:16]))
	}
	return codes, nil
}

// NormaliseRecoveryCode canonicalises user input so a code is accepted whether
// or not the dashes and original case are preserved.
func NormaliseRecoveryCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}
