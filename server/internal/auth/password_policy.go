package auth

import (
	"fmt"
	"unicode/utf8"
)

// MinPasswordLen is the minimum length, in characters, of a local account
// password. The GUI enforces the same value in every password form.
const MinPasswordLen = 8

// ValidatePassword checks a new local account password against the password
// policy. Every path that sets a password (setup, user create and update,
// self-service change, reset) must call it, so the policy cannot differ by
// path. The returned error is safe to show to the user.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	return nil
}
