package logretention

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

type fakeLogStore struct {
	pruneCalls []pruneCall
	reclaimed  int
	deleted    int64 // rows to report deleted per prune call
}

type pruneCall struct {
	levels []string
	before time.Time
}

func (f *fakeLogStore) PruneLogsByLevel(_ context.Context, levels []string, before time.Time, _ int) (int64, error) {
	f.pruneCalls = append(f.pruneCalls, pruneCall{levels: levels, before: before})
	return f.deleted, nil
}

func (f *fakeLogStore) ReclaimLogSpace(_ context.Context) error {
	f.reclaimed++
	return nil
}

type fakeSettings map[string]string

func (f fakeSettings) GetMany(_ context.Context, prefix string) ([]db.Setting, error) {
	var out []db.Setting
	for k, v := range f {
		out = append(out, db.Setting{Key: k, Value: db.EncryptedString(v)})
	}
	return out, nil
}

func TestRunOnce_DisabledByDefault(t *testing.T) {
	logs := &fakeLogStore{}
	svc := NewService(logs, fakeSettings{}, zap.NewNop())

	deleted, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
	if len(logs.pruneCalls) != 0 {
		t.Errorf("prune called %d times, want 0 (retention disabled)", len(logs.pruneCalls))
	}
}

func TestRunOnce_PrunesOnlyInfoWhenOnlyInfoConfigured(t *testing.T) {
	logs := &fakeLogStore{deleted: 5}
	svc := NewService(logs, fakeSettings{KeyInfoRetentionDays: "30"}, zap.NewNop())

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(logs.pruneCalls) != 1 {
		t.Fatalf("prune calls = %d, want 1", len(logs.pruneCalls))
	}
	if got := logs.pruneCalls[0].levels; len(got) != 1 || got[0] != "info" {
		t.Errorf("pruned levels = %v, want [info]", got)
	}
	// 5 deleted rows is below vacuumThreshold, so no reclaim.
	if logs.reclaimed != 0 {
		t.Errorf("reclaimed = %d, want 0 (below threshold)", logs.reclaimed)
	}
}

func TestRunOnce_PrunesWarnErrorWhenConfigured(t *testing.T) {
	logs := &fakeLogStore{}
	svc := NewService(logs, fakeSettings{
		KeyInfoRetentionDays:      "30",
		KeyWarnErrorRetentionDays: "365",
	}, zap.NewNop())

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(logs.pruneCalls) != 2 {
		t.Fatalf("prune calls = %d, want 2", len(logs.pruneCalls))
	}
	if got := logs.pruneCalls[1].levels; len(got) != 2 || got[0] != "warn" || got[1] != "error" {
		t.Errorf("second prune levels = %v, want [warn error]", got)
	}
}

func TestRunOnce_ReclaimsAboveThreshold(t *testing.T) {
	logs := &fakeLogStore{deleted: vacuumThreshold}
	svc := NewService(logs, fakeSettings{KeyInfoRetentionDays: "30"}, zap.NewNop())

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if logs.reclaimed != 1 {
		t.Errorf("reclaimed = %d, want 1 (deleted >= threshold)", logs.reclaimed)
	}
}
