package auth

import (
	"errors"
	"slices"
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

func TestGroupsFromClaim(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		want      []string
		wantFound bool
	}{
		{"list of names", []any{"arkeep-users", "/arkeep-admins"}, []string{"arkeep-users", "/arkeep-admins"}, true},
		{"list skips non-strings and empty names", []any{"a", 1, "", nil}, []string{"a"}, true},
		{"single name", "arkeep-users", []string{"arkeep-users"}, true},
		{"object keys", map[string]any{"arkeep-admins": map[string]any{"org": "x"}}, []string{"arkeep-admins"}, true},
		{"empty list", []any{}, nil, false},
		{"empty string", "", nil, false},
		{"absent claim", nil, nil, false},
		{"unsupported type", 42.0, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := groupsFromClaim(tt.in)
			if found != tt.wantFound || !slices.Equal(got, tt.want) {
				t.Errorf("groupsFromClaim(%#v) = %v, %v; want %v, %v", tt.in, got, found, tt.want, tt.wantFound)
			}
		})
	}
}

func TestDecideGroups(t *testing.T) {
	tests := []struct {
		name        string
		allowed     string
		admin       string
		userGroups  []string
		wantAllowed bool
		wantRole    string
	}{
		{"no group settings", "", "", nil, true, ""},
		{"in an allowed group", "arkeep-users, arkeep-ops", "", []string{"arkeep-ops"}, true, ""},
		{"in no allowed group", "arkeep-users", "", []string{"other"}, false, ""},
		{"groups claim missing", "arkeep-users", "", nil, false, ""},
		{"names are compared exactly", "arkeep-users", "", []string{"Arkeep-Users", "/arkeep-users"}, false, ""},
		{"admin group implies allowed", "arkeep-users", "arkeep-admins", []string{"arkeep-admins"}, true, "admin"},
		{"allowed but not admin", "arkeep-users", "arkeep-admins", []string{"arkeep-users"}, true, "user"},
		{"only admin groups, member", "", "arkeep-admins", []string{"arkeep-admins"}, true, "admin"},
		{"only admin groups, not member", "", "arkeep-admins", []string{"other"}, true, "user"},
		{"only admin groups, claim missing", "", "arkeep-admins", nil, true, "user"},
		{"blank entries are ignored", " , ", " ,", nil, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, role := decideGroups(tt.allowed, tt.admin, tt.userGroups)
			if allowed != tt.wantAllowed || role != tt.wantRole {
				t.Errorf("decideGroups(%q, %q, %v) = %v, %q; want %v, %q",
					tt.allowed, tt.admin, tt.userGroups, allowed, role, tt.wantAllowed, tt.wantRole)
			}
		})
	}
}
