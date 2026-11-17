package integration_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestReportDestinationStatus_SnapshotSummary checks that the restic summary
// an agent reports with a backup is stored on the snapshot record it creates,
// and that a report without one (an agent that predates summaries) still
// creates the record, with the summary unknown.
func TestReportDestinationStatus_SnapshotSummary(t *testing.T) {
	start := time.Date(2026, 11, 17, 8, 46, 7, 0, time.UTC)
	tests := []struct {
		name    string
		summary *proto.SnapshotSummary
	}{
		{"with summary", &proto.SnapshotSummary{
			BackupStart:         timestamppb.New(start),
			BackupEnd:           timestamppb.New(start.Add(90 * time.Second)),
			FilesNew:            1,
			FilesChanged:        2,
			FilesUnmodified:     3,
			TotalFilesProcessed: 6,
			TotalBytesProcessed: 4096,
			DataAdded:           3066,
			DataAddedPacked:     2500,
		}},
		{"without summary", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReconcileFixture(t)
			ctx := context.Background()

			if _, err := f.agent.client.ReportDestinationStatus(ctx, &proto.DestinationStatusReport{
				JobId:         f.jobID.String(),
				AgentId:       f.agent.agentID,
				DestinationId: f.destID.String(),
				Status:        "succeeded",
				SnapshotId:    "snap-1",
				SizeBytes:     2500,
				StartedAt:     timestamppb.New(start),
				Summary:       tt.summary,
			}); err != nil {
				t.Fatalf("ReportDestinationStatus: %v", err)
			}

			rows, _, err := f.ts.snapshotRepo.ListByDestination(ctx, f.destID, repositories.ListOptions{Limit: 10})
			if err != nil {
				t.Fatalf("list snapshots: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("stored %d snapshots, want 1", len(rows))
			}
			got := rows[0]
			if got.SizeBytes != 2500 {
				t.Errorf("SizeBytes = %d, want 2500", got.SizeBytes)
			}

			if tt.summary == nil {
				if got.BackupEnd != nil || got.FileCount != 0 {
					t.Errorf("summary = %+v, file count %d; want unknown", got.SnapshotSummary, got.FileCount)
				}
				return
			}
			if got.BackupStart == nil || !got.BackupStart.Equal(start) ||
				got.BackupEnd == nil || !got.BackupEnd.Equal(start.Add(90*time.Second)) {
				t.Errorf("backup = %v - %v, want %v - %v", got.BackupStart, got.BackupEnd, start, start.Add(90*time.Second))
			}
			if got.FilesNew != 1 || got.FilesChanged != 2 || got.FilesUnmodified != 3 ||
				got.FileCount != 6 || got.TotalBytesProcessed != 4096 || got.DataAdded != 3066 {
				t.Errorf("figures = %+v, file count %d", got.SnapshotSummary, got.FileCount)
			}
		})
	}
}
