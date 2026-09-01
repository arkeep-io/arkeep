package db

import (
	"bytes"
	"database/sql"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"
)

// TestSQLiteMigrationsDownUp walks every SQLite migration all the way down and
// back up with data present, so the table rebuilds the CHECK-constraint
// migrations rely on are exercised in both directions rather than assumed.
func TestSQLiteMigrationsDownUp(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/down.db",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// An interrupted job with an interrupted destination row: only the migrated
	// CHECK constraints allow these values, and the down migration has to fold
	// them back into 'failed' before narrowing the constraint again.
	agent := &Agent{Name: "agent", Status: "online", Labels: "{}"}
	if err := gdb.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	policy := &Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`, RepoPassword: EncryptedString("x")}
	if err := gdb.Create(policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	dest := &Destination{Name: "d", Type: "local", Config: `{"path":"/tmp/r"}`, Enabled: true}
	if err := gdb.Create(dest).Error; err != nil {
		t.Fatalf("create destination: %v", err)
	}
	job := &Job{PolicyID: policy.ID, AgentID: agent.ID, Type: "backup", Status: "interrupted"}
	if err := gdb.Create(job).Error; err != nil {
		t.Fatalf("create interrupted job: %v", err)
	}
	if err := gdb.Create(&JobDestination{JobID: job.ID, DestinationID: dest.ID, Status: "interrupted"}).Error; err != nil {
		t.Fatalf("create interrupted job destination: %v", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	m := newSQLiteMigrator(t, sqlDB)
	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Down: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Up after Down: %v", err)
	}

	// There is no down migration for the initial schema, so Down stops at
	// version 1 and the rows survive the round trip. That makes them the evidence
	// that the down migration folded the statuses its narrower CHECK constraint
	// cannot hold: 'interrupted' must have become 'failed', on the job and on its
	// destination row.
	var jobStatus, destStatus string
	if err := gdb.Raw(`SELECT status FROM jobs WHERE id = ?`, job.ID).Scan(&jobStatus).Error; err != nil {
		t.Fatalf("query job after down/up: %v", err)
	}
	if jobStatus != "failed" {
		t.Errorf("job status after down/up = %q, want %q: the down migration did not fold 'interrupted'", jobStatus, "failed")
	}
	if err := gdb.Raw(`SELECT status FROM job_destinations WHERE job_id = ?`, job.ID).Scan(&destStatus).Error; err != nil {
		t.Fatalf("query job destination after down/up: %v", err)
	}
	if destStatus != "failed" {
		t.Errorf("job destination status after down/up = %q, want %q", destStatus, "failed")
	}

	// And the re-migrated schema is usable, accepting 'interrupted' again.
	if err := gdb.Model(&Job{}).Where("id = ?", job.ID).Update("status", "interrupted").Error; err != nil {
		t.Errorf("cannot set 'interrupted' after down/up: %v", err)
	}
}

// TestInterruptedStatusIsAccepted pins the migrated CHECK constraints: both the
// job and its destination rows must accept 'interrupted'.
func TestInterruptedStatusIsAccepted(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{Driver: "sqlite", DSN: ":memory:", Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	agent := &Agent{Name: "agent", Status: "online", Labels: "{}"}
	if err := gdb.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	policy := &Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`, RepoPassword: EncryptedString("x")}
	if err := gdb.Create(policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	dest := &Destination{Name: "d", Type: "local", Config: `{}`, Enabled: true}
	if err := gdb.Create(dest).Error; err != nil {
		t.Fatalf("create destination: %v", err)
	}

	prior := uuid.New()
	job := &Job{
		PolicyID:      policy.ID,
		AgentID:       agent.ID,
		Type:          "backup",
		Status:        "interrupted",
		ResumeOfJobID: &prior,
		ResumeAttempt: 2,
		EndedAt:       func() *time.Time { n := time.Now().UTC(); return &n }(),
	}
	if err := gdb.Create(job).Error; err != nil {
		t.Fatalf("create interrupted job: %v", err)
	}
	if err := gdb.Create(&JobDestination{JobID: job.ID, DestinationID: dest.ID, Status: "interrupted"}).Error; err != nil {
		t.Fatalf("create interrupted job destination: %v", err)
	}

	var stored Job
	if err := gdb.First(&stored, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if stored.ResumeOfJobID == nil || *stored.ResumeOfJobID != prior {
		t.Errorf("ResumeOfJobID = %v, want %s", stored.ResumeOfJobID, prior)
	}
	if stored.ResumeAttempt != 2 {
		t.Errorf("ResumeAttempt = %d, want 2", stored.ResumeAttempt)
	}
}

func newSQLiteMigrator(t *testing.T, sqlDB *sql.DB) *migrate.Migrate {
	t.Helper()
	src, err := iofs.New(overlayFS{migrationsFS, "sqlite"}, "migrations")
	if err != nil {
		t.Fatalf("iofs.New: %v", err)
	}
	drv, err := migratesqlite.WithInstance(sqlDB, &migratesqlite.Config{NoTxWrap: true})
	if err != nil {
		t.Fatalf("migrate driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		t.Fatalf("migrate.NewWithInstance: %v", err)
	}
	return m
}
