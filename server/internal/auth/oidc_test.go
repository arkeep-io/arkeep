package auth

import (
	"errors"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func TestCanLinkByEmail(t *testing.T) {
	const provider = "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name          string
		user          db.User
		emailVerified bool
		wantRefused   bool
	}{
		{"verified local user", db.User{Role: "user"}, true, false},
		{"already linked to the same provider", db.User{Role: "user", OIDCProvider: provider}, true, false},
		{"unverified email", db.User{Role: "user"}, false, true},
		{"admin account", db.User{Role: "admin"}, true, true},
		{"linked to another provider", db.User{Role: "user", OIDCProvider: "22222222-2222-2222-2222-222222222222"}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := canLinkByEmail(&tt.user, provider, tt.emailVerified)
			if refused := errors.Is(err, ErrOIDCLinkRefused); refused != tt.wantRefused {
				t.Errorf("canLinkByEmail() error = %v, want refused=%v", err, tt.wantRefused)
			}
		})
	}
}

func TestClaimIsTrue(t *testing.T) {
	tests := []struct {
		in   any
		want bool
	}{
		{true, true},
		{false, false},
		{"true", true},
		{"TRUE", true},
		{"false", false},
		{nil, false},
		{1, false},
	}
	for _, tt := range tests {
		if got := claimIsTrue(tt.in); got != tt.want {
			t.Errorf("claimIsTrue(%#v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
