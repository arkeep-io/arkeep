package integration_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/snapshotsync"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// syncOutcome is what SyncDestination returned on the server side.
type syncOutcome struct {
	res snapshotsync.Result
	err error
}

// TestSnapshotSync_ReconcilesWithRepositoryListing drives an on-demand sync
// end to end over gRPC (issue #288): the server asks the connected agent to
// list the repository, the agent answers with a listing that no longer holds
// a snapshot pruned outside arkeep but holds one arkeep never saw, and the
// records are brought in line with it.
func TestSnapshotSync_ReconcilesWithRepositoryListing(t *testing.T) {
	f := newReconcileFixture(t)
	ctx := context.Background()

	// Attach the job's policy to the destination so its agent is eligible to
	// list the repository, and store the repository password.
	job, err := f.ts.jobRepo.GetByID(ctx, f.jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if err := f.ts.policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: *job.PolicyID, DestinationID: f.destID}); err != nil {
		t.Fatalf("attach policy: %v", err)
	}
	dest, err := f.ts.destRepo.GetByID(ctx, f.destID)
	if err != nil {
		t.Fatalf("get destination: %v", err)
	}
	dest.RepoPassword = db.EncryptedString("secret")
	if err := f.ts.destRepo.Update(ctx, dest); err != nil {
		t.Fatalf("update destination: %v", err)
	}

	old := time.Now().UTC().Add(-24 * time.Hour)
	f.seedSnapshot(t, f.destID, "kept", old)
	f.seedSnapshot(t, f.destID, "pruned-externally", old)

	jobs, cancel := f.agent.openStream(t)
	t.Cleanup(cancel)
	pollUntil(t, 3*time.Second, func() bool { return f.ts.agentMgr.IsConnected(f.agent.agentID) })

	svc := snapshotsync.NewService(f.ts.destRepo, f.ts.snapshotRepo, nil, f.ts.agentMgr, zap.NewNop())
	outcome := make(chan syncOutcome, 1)
	go func() {
		res, err := svc.SyncDestination(ctx, f.destID)
		outcome <- syncOutcome{res, err}
	}()

	var assignment *proto.JobAssignment
	select {
	case assignment = <-jobs:
	case <-time.After(3 * time.Second):
		t.Fatal("the agent received no listing request")
	}
	if assignment.Type != proto.JobType_JOB_TYPE_IMPORT_SNAPSHOTS {
		t.Fatalf("assignment type = %v, want IMPORT_SNAPSHOTS", assignment.Type)
	}

	if _, err := f.agent.client.ReportSnapshotImport(ctx, &proto.SnapshotImportReport{
		AgentId:       f.agent.agentID,
		CorrelationId: assignment.JobId,
		Snapshots: []*proto.ImportedSnapshotInfo{
			{ResticSnapshotId: "kept", SnapshotTime: old.Format(time.RFC3339Nano)},
			{ResticSnapshotId: "added-externally", SnapshotTime: old.Format(time.RFC3339Nano)},
		},
	}); err != nil {
		t.Fatalf("ReportSnapshotImport: %v", err)
	}

	var got syncOutcome
	select {
	case got = <-outcome:
	case <-time.After(3 * time.Second):
		t.Fatal("SyncDestination did not return")
	}
	if got.err != nil {
		t.Fatalf("SyncDestination: %v", got.err)
	}
	if got.res.Found != 2 || got.res.Imported != 1 || got.res.Removed != 1 || got.res.Failed != 0 {
		t.Errorf("result = %+v, want Found 2, Imported 1, Removed 1", got.res)
	}

	ids := f.remainingIDs(t, f.destID)
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "added-externally" || ids[1] != "kept" {
		t.Errorf("remaining = %v, want [added-externally kept]", ids)
	}
}
