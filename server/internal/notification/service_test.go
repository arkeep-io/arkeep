package notification

import (
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func TestIsEventEnabled(t *testing.T) {
	on := NotificationEventsConfig{JobSuccess: true, JobFailure: true, AgentOffline: true, AgentOnline: true}
	off := NotificationEventsConfig{}

	tests := []struct {
		name      string
		cfg       NotificationEventsConfig
		eventType string
		override  string
		want      bool
	}{
		{"inherit follows global on", on, "job_success", db.NotifyInherit, true},
		{"inherit follows global off", off, "job_failure", db.NotifyInherit, false},
		{"always beats global off", off, "job_success", db.NotifyAlways, true},
		{"never beats global on", on, "job_failure", db.NotifyNever, false},
		{"agent event without override follows global", off, "agent_offline", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEventEnabled(tt.cfg, tt.eventType, tt.override); got != tt.want {
				t.Errorf("isEventEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
