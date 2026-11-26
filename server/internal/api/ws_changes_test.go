package api

import (
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/auth"
)

// TestConnectionTopics: every connection gets the changes topic without
// asking, and only an admin's gets the admin changes topic.
func TestConnectionTopics(t *testing.T) {
	tests := []struct {
		role     string
		in, want []string
	}{
		{"user", []string{"notifications:u1"}, []string{"notifications:u1", "changes"}},
		{"user", []string{"notifications:u1", "job:j1"}, []string{"notifications:u1", "job:j1", "changes"}},
		{"admin", []string{"notifications:u1"}, []string{"notifications:u1", "changes", "changes:admin"}},
	}
	for _, tt := range tests {
		if got := connectionTopics(slices.Clone(tt.in), tt.role); !slices.Equal(got, tt.want) {
			t.Errorf("connectionTopics(%q, %s) = %q, want %q", tt.in, tt.role, got, tt.want)
		}
	}
}

// TestResolveTopics_ChangesNotAsked: a client cannot ask for the changes
// topics, so a user never gets the admin one.
func TestResolveTopics_ChangesNotAsked(t *testing.T) {
	const self = "00000000-0000-0000-0000-00000000000a"
	topics := url.QueryEscape("job:1,changes:admin, changes,agent:2")
	r := httptest.NewRequest("GET", "/api/v1/ws?topics="+topics, nil)
	claims := &auth.Claims{UserID: self, Role: "user"}

	got := connectionTopics((&WSHandler{}).resolveTopics(r, claims), claims.Role)
	want := []string{"notifications:" + self, "job:1", "agent:2", "changes"}
	if !slices.Equal(got, want) {
		t.Errorf("topics = %v, want %v", got, want)
	}
}
