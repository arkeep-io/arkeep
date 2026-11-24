package api

import (
	"slices"
	"testing"
)

// TestConnectionTopics: every connection gets the changes topic without
// asking, once, after the topics resolveTopics resolved.
func TestConnectionTopics(t *testing.T) {
	tests := []struct {
		in, want []string
	}{
		{[]string{"notifications:u1"}, []string{"notifications:u1", "changes"}},
		{[]string{"notifications:u1", "job:j1"}, []string{"notifications:u1", "job:j1", "changes"}},
		{[]string{"notifications:u1", "changes", "job:j1"}, []string{"notifications:u1", "changes", "job:j1"}},
	}
	for _, tt := range tests {
		if got := connectionTopics(slices.Clone(tt.in)); !slices.Equal(got, tt.want) {
			t.Errorf("connectionTopics(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
