package auth

import "testing"

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		password string
		valid    bool
	}{
		{"", false},
		{"1234567", false},
		{"12345678", true},
		{"àèìòùàèì", true}, // 8 characters, 16 bytes
		{"àèìòùàè", false}, // 7 characters, 14 bytes
	}
	for _, tt := range tests {
		if err := ValidatePassword(tt.password); (err == nil) != tt.valid {
			t.Errorf("ValidatePassword(%q) error = %v, want valid=%v", tt.password, err, tt.valid)
		}
	}
}
