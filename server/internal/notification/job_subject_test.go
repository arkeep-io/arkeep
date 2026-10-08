package notification

import (
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

func TestJobSubjectDescribe(t *testing.T) {
	tests := []struct {
		name          string
		job           JobSubject
		wantOperation string
		wantName      string
		wantSubject   string
	}{
		{"backup", JobSubject{Type: "backup", PolicyName: "nightly"}, "Backup", "nightly", `Policy "nightly"`},
		{"legacy empty type", JobSubject{PolicyName: "nightly"}, "Backup", "nightly", `Policy "nightly"`},
		{"restore", JobSubject{Type: "restore", PolicyName: "nightly"}, "Restore", "nightly", `Restore of policy "nightly"`},
		{"retention", JobSubject{Type: "retention", DestinationName: "nas"}, "Retention", "nas", `Retention of destination "nas"`},
		{"check", JobSubject{Type: "check", DestinationName: "nas"}, "Integrity check", "nas", `Integrity check of destination "nas"`},
		{"header injection", JobSubject{Type: "check", DestinationName: "nas\r\nBcc: x"}, "Integrity check", "nasBcc: x", `Integrity check of destination "nasBcc: x"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, name, subject := tt.job.describe()
			if op != tt.wantOperation || name != tt.wantName || subject != tt.wantSubject {
				t.Errorf("describe() = (%q, %q, %q), want (%q, %q, %q)",
					op, name, subject, tt.wantOperation, tt.wantName, tt.wantSubject)
			}
		})
	}
}

func TestJobSubjectFrom(t *testing.T) {
	policyID := uuid.Must(uuid.NewV7())
	backup := &repositories.JobWithNames{Job: db.Job{Type: "backup", PolicyID: &policyID}, PolicyName: "nightly"}
	two := []repositories.JobDestinationWithName{{DestinationName: "a"}, {DestinationName: "b"}}
	got := JobSubjectFrom(backup, two)
	if got.PolicyID != policyID || got.PolicyName != "nightly" || got.DestinationName != "" {
		t.Errorf("JobSubjectFrom(backup) = %+v, want policy set and no destination name", got)
	}

	check := &repositories.JobWithNames{Job: db.Job{Type: "check"}}
	got = JobSubjectFrom(check, []repositories.JobDestinationWithName{{DestinationName: "nas"}})
	if got.PolicyID != uuid.Nil || got.Type != "check" || got.DestinationName != "nas" {
		t.Errorf("JobSubjectFrom(check) = %+v, want nil policy and destination \"nas\"", got)
	}
}
