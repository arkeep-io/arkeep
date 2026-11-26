package api

import (
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/auth"
)

// TestResolveTopics_NotificationsOfOthers: a client always gets its own
// notification topic, and never another user's, even when it asks for it.
func TestResolveTopics_NotificationsOfOthers(t *testing.T) {
	const self = "00000000-0000-0000-0000-00000000000a"
	const other = "00000000-0000-0000-0000-00000000000b"

	topics := url.QueryEscape("job:1,notifications:" + other + ", notifications:" + self + ",agent:2")
	r := httptest.NewRequest("GET", "/api/v1/ws?topics="+topics, nil)
	got := (&WSHandler{}).resolveTopics(r, &auth.Claims{UserID: self})

	want := []string{"notifications:" + self, "job:1", "agent:2"}
	if !slices.Equal(got, want) {
		t.Errorf("topics = %v, want %v", got, want)
	}
}
