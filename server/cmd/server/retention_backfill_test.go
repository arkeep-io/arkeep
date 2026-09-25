package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// failingUpdateDestRepo rejects every Update, mimicking #267 where PostgreSQL
// refused to encode the destination's bool flags into int4 columns.
type failingUpdateDestRepo struct {
	repositories.DestinationRepository
}

func (failingUpdateDestRepo) Update(context.Context, *db.Destination) error {
	return errors.New("unable to encode true into binary format for int4")
}

func newBackfillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	gormDB, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return gormDB
}

// seedSinglePolicyDestination creates a destination attached to exactly one
// policy with legacy retention values, the case the backfill copies over.
func seedSinglePolicyDestination(t *testing.T, gormDB *gorm.DB) *db.Destination {
	t.Helper()
	ctx := context.Background()
	agent := &db.Agent{Name: "agent", Status: "offline", Labels: "{}"}
	if err := repositories.NewAgentRepository(gormDB).Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	dest := &db.Destination{Name: "dest", Type: "local", Config: "{}", Enabled: true}
	if err := repositories.NewDestinationRepository(gormDB).Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	policyRepo := repositories.NewPolicyRepository(gormDB)
	p := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`}
	if err := policyRepo.Create(ctx, p); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := gormDB.Exec("UPDATE policies SET retention_daily = 7 WHERE id = ?", p.ID).Error; err != nil {
		t.Fatalf("set legacy retention: %v", err)
	}
	if err := policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: p.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach policy: %v", err)
	}
	return dest
}

func TestBackfillDestinationRetention_UpdateFailureDoesNotMarkDone(t *testing.T) {
	gormDB := newBackfillTestDB(t)
	ctx := context.Background()
	dest := seedSinglePolicyDestination(t, gormDB)
	destRepo := repositories.NewDestinationRepository(gormDB)
	settingsRepo := repositories.NewSettingsRepository(gormDB)

	err := backfillDestinationRetention(ctx, gormDB, failingUpdateDestRepo{destRepo}, settingsRepo, zap.NewNop())
	if err == nil {
		t.Fatal("expected an error when a destination update fails")
	}
	if _, err := settingsRepo.Get(ctx, retentionBackfillSettingKey); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("marker must not be written after a failed run, Get err = %v", err)
	}

	// Next start, with a working repository, retries and completes.
	if err := backfillDestinationRetention(ctx, gormDB, destRepo, settingsRepo, zap.NewNop()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := settingsRepo.Get(ctx, retentionBackfillSettingKey); err != nil {
		t.Fatalf("marker should be written after a successful run: %v", err)
	}
	got, err := destRepo.GetByID(ctx, dest.ID)
	if err != nil {
		t.Fatalf("get destination: %v", err)
	}
	if !got.RetentionEnabled || got.RetentionDaily != 7 || got.RetentionSchedule == "" {
		t.Fatalf("destination not backfilled: enabled=%v daily=%d schedule=%q",
			got.RetentionEnabled, got.RetentionDaily, got.RetentionSchedule)
	}
}

func TestBackfillDestinationRetention_RetryKeepsConfiguredDestination(t *testing.T) {
	gormDB := newBackfillTestDB(t)
	ctx := context.Background()
	dest := seedSinglePolicyDestination(t, gormDB)
	destRepo := repositories.NewDestinationRepository(gormDB)

	// Already configured (by an earlier partial run or an admin): a retry
	// must not overwrite it with the legacy policy values.
	dest.RetentionSchedule = "0 4 * * 0"
	dest.RetentionDaily = 30
	dest.RetentionEnabled = true
	if err := destRepo.Update(ctx, dest); err != nil {
		t.Fatalf("configure destination: %v", err)
	}

	if err := backfillDestinationRetention(ctx, gormDB, destRepo, repositories.NewSettingsRepository(gormDB), zap.NewNop()); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got, err := destRepo.GetByID(ctx, dest.ID)
	if err != nil {
		t.Fatalf("get destination: %v", err)
	}
	if got.RetentionSchedule != "0 4 * * 0" || got.RetentionDaily != 30 {
		t.Fatalf("configured destination overwritten: schedule=%q daily=%d", got.RetentionSchedule, got.RetentionDaily)
	}
}
