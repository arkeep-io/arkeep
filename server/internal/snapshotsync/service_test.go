package snapshotsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// fakeLister stands in for the agent manager: it reports a fixed set of agents
// as connected and answers every listing with a canned result.
type fakeLister struct {
	connected map[string]bool
	result    agentmanager.SnapshotImportResult
	err       error

	calls       int
	lastAgent   string
	lastPayload ImportPayload
}

func (f *fakeLister) IsConnected(agentID string) bool { return f.connected[agentID] }

func (f *fakeLister) RequestSnapshotImport(_ context.Context, agentID, _ string, payloadJSON []byte) (agentmanager.SnapshotImportResult, error) {
	f.calls++
	f.lastAgent = agentID
	if err := json.Unmarshal(payloadJSON, &f.lastPayload); err != nil {
		return agentmanager.SnapshotImportResult{}, err
	}
	return f.result, f.err
}

// listing builds an agent listing holding the given restic snapshot IDs.
func listing(ids ...string) agentmanager.SnapshotImportResult {
	res := agentmanager.SnapshotImportResult{}
	for _, id := range ids {
		res.Snapshots = append(res.Snapshots, &proto.ImportedSnapshotInfo{
			ResticSnapshotId: id,
			SnapshotTime:     "2026-07-26T13:20:45.123456789+02:00",
			Paths:            []string{"/data"},
			Tags:             []string{},
			Hostname:         "nas",
			SizeBytes:        2048,
			FileCount:        7,
		})
	}
	return res
}

type testEnv struct {
	gdb      *gorm.DB
	dests    repositories.DestinationRepository
	snaps    repositories.SnapshotRepository
	policies repositories.PolicyRepository
	settings repositories.SettingsRepository
	agent    *db.Agent
	lister   *fakeLister
	svc      *Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	gdb, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	e := &testEnv{
		gdb:      gdb,
		dests:    repositories.NewDestinationRepository(gdb),
		snaps:    repositories.NewSnapshotRepository(gdb),
		policies: repositories.NewPolicyRepository(gdb),
		settings: repositories.NewSettingsRepository(gdb),
		lister:   &fakeLister{connected: map[string]bool{}},
	}
	e.agent = e.createAgent(t, "agent")
	e.svc = NewService(e.dests, e.snaps, e.settings, e.lister, zap.NewNop())
	return e
}

func (e *testEnv) createAgent(t *testing.T, name string) *db.Agent {
	t.Helper()
	agent := &db.Agent{Name: name, Hostname: name, Status: "online", Labels: "{}"}
	if err := repositories.NewAgentRepository(e.gdb).Create(context.Background(), agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

// createDestination inserts an enabled destination with a stored repository password.
func (e *testEnv) createDestination(t *testing.T, name string) *db.Destination {
	t.Helper()
	dest := &db.Destination{
		Name:         name,
		Type:         "rest",
		Credentials:  db.EncryptedString(`{"url":"rest:http://nas:8000/repo"}`),
		Config:       "{}",
		Enabled:      true,
		RepoPassword: db.EncryptedString("dest-secret"),
	}
	if err := e.dests.Create(context.Background(), dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	return dest
}

// attachPolicy creates a policy on the given agent writing to dest.
func (e *testEnv) attachPolicy(t *testing.T, agent *db.Agent, dest *db.Destination, password string) *db.Policy {
	t.Helper()
	ctx := context.Background()
	p := &db.Policy{Name: "p-" + uuid.NewString(), AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`, RepoPassword: db.EncryptedString(password)}
	if err := e.policies.Create(ctx, p); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := e.policies.AddDestination(ctx, &db.PolicyDestination{PolicyID: p.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach policy: %v", err)
	}
	return p
}

// createSnapshot records an imported snapshot for dest taken at the given time.
func (e *testEnv) createSnapshot(t *testing.T, dest *db.Destination, snapshotID string, at time.Time) {
	t.Helper()
	snap := &db.Snapshot{
		DestinationID: dest.ID,
		IsImported:    true,
		SnapshotID:    snapshotID,
		Tags:          "[]",
		Sources:       "[]",
		SnapshotAt:    at,
	}
	if err := e.snaps.Create(context.Background(), snap); err != nil {
		t.Fatalf("create snapshot %s: %v", snapshotID, err)
	}
}

// snapshotIDs returns the restic snapshot IDs recorded for dest, sorted.
func (e *testEnv) snapshotIDs(t *testing.T, dest *db.Destination) []string {
	t.Helper()
	var snaps []db.Snapshot
	if err := e.gdb.Where("destination_id = ?", dest.ID).Order("snapshot_id ASC").Find(&snaps).Error; err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	ids := make([]string, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.SnapshotID)
	}
	return ids
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSyncDestination(t *testing.T) {
	old := time.Now().UTC().Add(-24 * time.Hour)

	t.Run("imports new snapshots and evicts pruned ones", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")
		e.lister.connected[e.agent.ID.String()] = true
		e.createSnapshot(t, dest, "kept", old)
		e.createSnapshot(t, dest, "pruned", old)
		e.lister.result = listing("kept", "new")

		res, err := e.svc.SyncDestination(context.Background(), dest.ID)
		if err != nil {
			t.Fatalf("SyncDestination: %v", err)
		}
		want := Result{Found: 2, ImportOutcome: ImportOutcome{Imported: 1, Skipped: 1}, Removed: 1}
		if res != want {
			t.Errorf("result = %+v, want %+v", res, want)
		}
		if got := e.snapshotIDs(t, dest); !equalIDs(got, []string{"kept", "new"}) {
			t.Errorf("remaining = %v, want [kept new]", got)
		}
	})

	t.Run("an empty repository evicts every record", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")
		e.lister.connected[e.agent.ID.String()] = true
		e.createSnapshot(t, dest, "pruned-1", old)
		e.createSnapshot(t, dest, "pruned-2", old)

		res, err := e.svc.SyncDestination(context.Background(), dest.ID)
		if err != nil {
			t.Fatalf("SyncDestination: %v", err)
		}
		if res.Removed != 2 || res.Found != 0 {
			t.Errorf("result = %+v, want Found 0, Removed 2", res)
		}
		if got := e.snapshotIDs(t, dest); len(got) != 0 {
			t.Errorf("remaining = %v, want none", got)
		}
	})

	t.Run("keeps records created after the listing started", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")
		e.lister.connected[e.agent.ID.String()] = true
		e.createSnapshot(t, dest, "from-running-backup", time.Now().UTC().Add(time.Hour))

		if _, err := e.svc.SyncDestination(context.Background(), dest.ID); err != nil {
			t.Fatalf("SyncDestination: %v", err)
		}
		if got := e.snapshotIDs(t, dest); !equalIDs(got, []string{"from-running-backup"}) {
			t.Errorf("remaining = %v, want [from-running-backup]", got)
		}
	})

	t.Run("prefers the retention agent", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")
		retention := e.createAgent(t, "retention")
		dest.RetentionAgentID = &retention.ID
		if err := e.dests.Update(context.Background(), dest); err != nil {
			t.Fatalf("update destination: %v", err)
		}
		e.lister.connected[e.agent.ID.String()] = true
		e.lister.connected[retention.ID.String()] = true

		if _, err := e.svc.SyncDestination(context.Background(), dest.ID); err != nil {
			t.Fatalf("SyncDestination: %v", err)
		}
		if e.lister.lastAgent != retention.ID.String() {
			t.Errorf("listed on agent %s, want the retention agent %s", e.lister.lastAgent, retention.ID)
		}
	})

	t.Run("falls back to a policy agent when the retention agent is offline", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		offline := e.createAgent(t, "offline")
		e.attachPolicy(t, offline, dest, "")
		e.attachPolicy(t, e.agent, dest, "")
		retention := e.createAgent(t, "retention")
		dest.RetentionAgentID = &retention.ID
		if err := e.dests.Update(context.Background(), dest); err != nil {
			t.Fatalf("update destination: %v", err)
		}
		e.lister.connected[e.agent.ID.String()] = true

		if _, err := e.svc.SyncDestination(context.Background(), dest.ID); err != nil {
			t.Fatalf("SyncDestination: %v", err)
		}
		if e.lister.lastAgent != e.agent.ID.String() {
			t.Errorf("listed on agent %s, want the online policy agent %s", e.lister.lastAgent, e.agent.ID)
		}
	})

	t.Run("fails when no agent is connected", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")

		_, err := e.svc.SyncDestination(context.Background(), dest.ID)
		if !errors.Is(err, ErrNoAgentAvailable) {
			t.Errorf("err = %v, want ErrNoAgentAvailable", err)
		}
		if e.lister.calls != 0 {
			t.Errorf("listing requested %d times, want 0", e.lister.calls)
		}
	})

	t.Run("uses the destination password", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "policy-secret")
		e.lister.connected[e.agent.ID.String()] = true

		if _, err := e.svc.SyncDestination(context.Background(), dest.ID); err != nil {
			t.Fatalf("SyncDestination: %v", err)
		}
		if got := e.lister.lastPayload.Env["RESTIC_PASSWORD"]; got != "dest-secret" {
			t.Errorf("RESTIC_PASSWORD = %q, want dest-secret", got)
		}
	})

	t.Run("falls back to a policy password", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		if err := e.gdb.Model(&db.Destination{}).Where("id = ?", dest.ID).Update("repo_password", "").Error; err != nil {
			t.Fatalf("clear destination password: %v", err)
		}
		e.attachPolicy(t, e.agent, dest, "")
		e.attachPolicy(t, e.agent, dest, "policy-secret")
		e.lister.connected[e.agent.ID.String()] = true

		if _, err := e.svc.SyncDestination(context.Background(), dest.ID); err != nil {
			t.Fatalf("SyncDestination: %v", err)
		}
		if got := e.lister.lastPayload.Env["RESTIC_PASSWORD"]; got != "policy-secret" {
			t.Errorf("RESTIC_PASSWORD = %q, want policy-secret", got)
		}
	})

	t.Run("fails when no password is known", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		if err := e.gdb.Model(&db.Destination{}).Where("id = ?", dest.ID).Update("repo_password", "").Error; err != nil {
			t.Fatalf("clear destination password: %v", err)
		}
		e.attachPolicy(t, e.agent, dest, "")
		e.lister.connected[e.agent.ID.String()] = true

		_, err := e.svc.SyncDestination(context.Background(), dest.ID)
		if !errors.Is(err, ErrNoRepoPassword) {
			t.Errorf("err = %v, want ErrNoRepoPassword", err)
		}
	})

	t.Run("refuses a busy destination", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")
		e.lister.connected[e.agent.ID.String()] = true
		e.createSnapshot(t, dest, "pruned", old)
		if err := e.gdb.Model(&db.Destination{}).Where("id = ?", dest.ID).Update("busy_job_id", uuid.New()).Error; err != nil {
			t.Fatalf("mark destination busy: %v", err)
		}

		_, err := e.svc.SyncDestination(context.Background(), dest.ID)
		if !errors.Is(err, ErrDestinationBusy) {
			t.Errorf("err = %v, want ErrDestinationBusy", err)
		}
		if got := e.snapshotIDs(t, dest); len(got) != 1 {
			t.Errorf("remaining = %v, want the record untouched", got)
		}
	})

	t.Run("a failed listing changes nothing", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")
		e.lister.connected[e.agent.ID.String()] = true
		e.createSnapshot(t, dest, "kept", old)
		e.lister.result = agentmanager.SnapshotImportResult{Err: "Fatal: wrong password"}

		_, err := e.svc.SyncDestination(context.Background(), dest.ID)
		var listingErr *ListingError
		if !errors.As(err, &listingErr) || listingErr.Message != "Fatal: wrong password" {
			t.Errorf("err = %v, want a ListingError carrying the restic message", err)
		}
		if got := e.snapshotIDs(t, dest); !equalIDs(got, []string{"kept"}) {
			t.Errorf("remaining = %v, want [kept]", got)
		}
	})

	t.Run("returns ErrNotFound for an unknown destination", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.svc.SyncDestination(context.Background(), uuid.New())
		if !errors.Is(err, repositories.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestPersistImported covers recording the snapshots of a repository listing.
// Such snapshots belong to no policy and no job, so they are stored with a nil
// policy_id / job_id.
func TestPersistImported(t *testing.T) {
	t.Run("persists every snapshot found", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "imported")
		res := listing("aaa111", "bbb222")

		out := e.svc.PersistImported(context.Background(), dest, &res)

		if out != (ImportOutcome{Imported: 2}) {
			t.Fatalf("outcome = %+v, want {Imported:2 Skipped:0 Failed:0}", out)
		}

		rows, total, err := e.snaps.ListByDestination(context.Background(), dest.ID, repositories.ListOptions{Limit: 10})
		if err != nil {
			t.Fatalf("ListByDestination: %v", err)
		}
		if total != 2 {
			t.Fatalf("stored %d snapshots, want 2", total)
		}
		for _, row := range rows {
			if row.PolicyID != nil {
				t.Errorf("snapshot %s: PolicyID = %v, want nil", row.SnapshotID, row.PolicyID)
			}
			if row.JobID != nil {
				t.Errorf("snapshot %s: JobID = %v, want nil", row.SnapshotID, row.JobID)
			}
			if !row.IsImported {
				t.Errorf("snapshot %s: IsImported = false, want true", row.SnapshotID)
			}
			if row.Hostname != "nas" {
				t.Errorf("snapshot %s: Hostname = %q, want %q", row.SnapshotID, row.Hostname, "nas")
			}
			if row.SnapshotAt.IsZero() {
				t.Errorf("snapshot %s: SnapshotAt is zero, want the parsed restic timestamp", row.SnapshotID)
			}
			if row.SizeBytes != 2048 {
				t.Errorf("snapshot %s: SizeBytes = %d, want 2048", row.SnapshotID, row.SizeBytes)
			}
			if row.FileCount != 7 {
				t.Errorf("snapshot %s: FileCount = %d, want 7", row.SnapshotID, row.FileCount)
			}
		}
	})

	t.Run("reports already known snapshots as skipped, not imported", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "imported")
		ctx := context.Background()

		first := listing("aaa111", "bbb222")
		if out := e.svc.PersistImported(ctx, dest, &first); out.Imported != 2 {
			t.Fatalf("first import: outcome = %+v, want 2 imported", out)
		}

		second := listing("aaa111", "bbb222", "ccc333")
		out := e.svc.PersistImported(ctx, dest, &second)

		if out != (ImportOutcome{Imported: 1, Skipped: 2}) {
			t.Fatalf("second import: outcome = %+v, want {Imported:1 Skipped:2 Failed:0}", out)
		}
	})

	t.Run("the same repository copied to another destination is importable again", func(t *testing.T) {
		// Migrating a repository between cloud providers yields two
		// destinations holding the same restic snapshot IDs.
		e := newTestEnv(t)
		oldDest := e.createDestination(t, "provider1")
		newDest := e.createDestination(t, "provider2")
		ctx := context.Background()
		res := listing("aaa111")

		if out := e.svc.PersistImported(ctx, oldDest, &res); out.Imported != 1 {
			t.Fatalf("import into provider1: outcome = %+v, want 1 imported", out)
		}

		out := e.svc.PersistImported(ctx, newDest, &res)

		if out != (ImportOutcome{Imported: 1}) {
			t.Fatalf("import into provider2: outcome = %+v, want {Imported:1 Skipped:0 Failed:0}", out)
		}
	})

	t.Run("caches the repository size reported by the agent", func(t *testing.T) {
		e := newTestEnv(t)
		dest := e.createDestination(t, "imported")

		res := listing("aaa111")
		res.RepoSizeBytes = 4096

		e.svc.PersistImported(context.Background(), dest, &res)

		stored, err := e.dests.GetByID(context.Background(), dest.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.RepoSizeBytes != 4096 {
			t.Errorf("RepoSizeBytes = %d, want 4096", stored.RepoSizeBytes)
		}
	})
}

func TestPeriodicSweep(t *testing.T) {
	setup := func(t *testing.T) *testEnv {
		e := newTestEnv(t)
		dest := e.createDestination(t, "nas")
		e.attachPolicy(t, e.agent, dest, "")
		disabled := e.createDestination(t, "disabled")
		e.attachPolicy(t, e.agent, disabled, "")
		if err := e.gdb.Model(&db.Destination{}).Where("id = ?", disabled.ID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable destination: %v", err)
		}
		e.lister.connected[e.agent.ID.String()] = true
		return e
	}

	t.Run("disabled by default", func(t *testing.T) {
		e := setup(t)
		e.svc.tick(context.Background())
		if e.lister.calls != 0 {
			t.Errorf("listings = %d, want 0 while no interval is configured", e.lister.calls)
		}
	})

	t.Run("syncs every enabled destination once per interval", func(t *testing.T) {
		e := setup(t)
		if err := e.settings.Set(context.Background(), KeyIntervalHours, db.EncryptedString("6")); err != nil {
			t.Fatalf("set interval: %v", err)
		}

		e.svc.tick(context.Background())
		if e.lister.calls != 1 {
			t.Fatalf("listings after first tick = %d, want 1 (the disabled destination is skipped)", e.lister.calls)
		}

		e.svc.tick(context.Background())
		if e.lister.calls != 1 {
			t.Errorf("listings after a tick within the interval = %d, want still 1", e.lister.calls)
		}

		e.svc.lastSweep = time.Now().Add(-7 * time.Hour)
		e.svc.tick(context.Background())
		if e.lister.calls != 2 {
			t.Errorf("listings after the interval elapsed = %d, want 2", e.lister.calls)
		}
	})
}
