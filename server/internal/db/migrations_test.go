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

// TestSQLiteMigrationsDownUp_InterruptedStatus walks every SQLite migration
// all the way down and back up with data present, so the table rebuilds the
// CHECK-constraint migrations rely on are exercised in both directions
// rather than assumed.
func TestSQLiteMigrationsDownUp_InterruptedStatus(t *testing.T) {
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
	job := &Job{PolicyID: &policy.ID, AgentID: agent.ID, Type: "backup", Status: "interrupted"}
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

// TestSQLiteMigrationsDownUp_NullablePolicyJob walks every SQLite migration
// all the way down and back up, with an imported snapshot present, so the
// table rebuilds in the 000019 down migration are exercised rather than
// assumed.
func TestSQLiteMigrationsDownUp_NullablePolicyJob(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	dsn := "file:" + t.TempDir() + "/down.db"
	gdb, err := New(Config{Driver: "sqlite", DSN: dsn, Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dest := &Destination{Name: "imported", Type: "rclone", Config: "{}", Enabled: true}
	if err := gdb.Create(dest).Error; err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := gdb.Create(&Snapshot{
		DestinationID: dest.ID, IsImported: true, SnapshotID: "deadbeef",
		Tags: "[]", Sources: `["/data"]`, SnapshotAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create imported snapshot: %v", err)
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

	// Schema is usable again after the round trip.
	var count int64
	if err := gdb.Raw(`SELECT count(*) FROM snapshots`).Scan(&count).Error; err != nil {
		t.Fatalf("query snapshots after down/up: %v", err)
	}
	if count != 0 {
		t.Errorf("snapshots after down/up = %d, want 0 (the down migration drops everything)", count)
	}
}

// TestSQLiteMigrationsDownUp_SkippedStatus walks every SQLite migration all
// the way down and back up with a 'skipped' job_destinations row present
// (issue #130's busy-gate deferral status), so the 000026 table rebuild is
// exercised in both directions rather than assumed.
func TestSQLiteMigrationsDownUp_SkippedStatus(t *testing.T) {
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
	job := &Job{PolicyID: &policy.ID, AgentID: agent.ID, Type: "backup", Status: "pending"}
	if err := gdb.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := gdb.Create(&JobDestination{JobID: job.ID, DestinationID: dest.ID, Status: "skipped"}).Error; err != nil {
		t.Fatalf("create skipped job destination: %v", err)
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

	// The down migration's narrower CHECK constraint cannot hold 'skipped', so
	// it must have folded the row back into 'failed'. Unlike job_destinations
	// (present since the initial schema), job_destination_commands is dropped
	// unconditionally by 000023's own down migration once Down() walks that
	// far back — its data cannot survive a full round trip to version 1 by
	// design (same as every other table introduced after the initial schema),
	// so that table is checked for usability only, below.
	var destStatus string
	if err := gdb.Raw(`SELECT status FROM job_destinations WHERE job_id = ?`, job.ID).Scan(&destStatus).Error; err != nil {
		t.Fatalf("query job destination after down/up: %v", err)
	}
	if destStatus != "failed" {
		t.Errorf("job destination status after down/up = %q, want %q: the down migration did not fold 'skipped'", destStatus, "failed")
	}

	// The re-migrated schema is usable, accepting 'skipped' again on both
	// tables the 000026 migration widens.
	if err := gdb.Model(&JobDestination{}).Where("job_id = ?", job.ID).Update("status", "skipped").Error; err != nil {
		t.Errorf("cannot set 'skipped' on job_destinations after down/up: %v", err)
	}
	if err := gdb.Create(&JobDestinationCommand{JobID: job.ID, DestinationID: dest.ID, SourceName: "dump", Status: "skipped"}).Error; err != nil {
		t.Errorf("cannot create a 'skipped' job destination command after down/up: %v", err)
	}
}

// TestSQLiteMigrationDownUp_WaitingStatus steps the 000032 migration down and
// back up with a waiting job and its child rows present (issue #285), so the
// jobs table rebuild is exercised in both directions: the down migration must
// fold 'waiting' into 'failed', and both rebuilds must keep every column and
// the child tables' foreign keys.
func TestSQLiteMigrationDownUp_WaitingStatus(t *testing.T) {
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
	prior := uuid.New()
	job := &Job{PolicyID: &policy.ID, AgentID: agent.ID, Type: "backup", Status: "waiting", ResumeOfJobID: &prior, ResumeAttempt: 2}
	if err := gdb.Create(job).Error; err != nil {
		t.Fatalf("create waiting job: %v", err)
	}
	if err := gdb.Create(&JobDestination{JobID: job.ID, DestinationID: dest.ID, Status: "pending"}).Error; err != nil {
		t.Fatalf("create job destination: %v", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	m := newSQLiteMigrator(t, sqlDB)
	// Migrate to the version right before 000032 rather than one step down,
	// so later migrations do not shift what this test exercises.
	if err := m.Migrate(31); err != nil {
		t.Fatalf("Migrate(31): %v", err)
	}

	var row struct {
		Status        string
		ResumeOfJobID string
		ResumeAttempt int
	}
	if err := gdb.Raw(`SELECT status, resume_of_job_id, resume_attempt FROM jobs WHERE id = ?`, job.ID).Scan(&row).Error; err != nil {
		t.Fatalf("query job after down: %v", err)
	}
	if row.Status != "failed" {
		t.Errorf("job status after down = %q, want %q: the down migration did not fold 'waiting'", row.Status, "failed")
	}
	if row.ResumeOfJobID != prior.String() || row.ResumeAttempt != 2 {
		t.Errorf("resume columns after down = (%q, %d), want (%q, 2)", row.ResumeOfJobID, row.ResumeAttempt, prior)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Up after down: %v", err)
	}
	if err := gdb.Model(&Job{}).Where("id = ?", job.ID).Update("status", "waiting").Error; err != nil {
		t.Errorf("cannot set 'waiting' after down/up: %v", err)
	}

	// The child table still points at the rebuilt jobs table: deleting the job
	// cascades to its destination rows.
	if err := gdb.Exec(`DELETE FROM jobs WHERE id = ?`, job.ID).Error; err != nil {
		t.Fatalf("delete job: %v", err)
	}
	var children int64
	if err := gdb.Model(&JobDestination{}).Where("job_id = ?", job.ID).Count(&children).Error; err != nil {
		t.Fatalf("count job destinations: %v", err)
	}
	if children != 0 {
		t.Errorf("job destinations after deleting the job = %d, want 0 (ON DELETE CASCADE lost in the rebuild)", children)
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
		PolicyID:      &policy.ID,
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

// TestSQLiteMigration_S3PrefixReset checks that 000034 drops config.prefix
// from S3 destinations only, keeps their other keys, and leaves a config that
// is not valid JSON untouched instead of failing the migration.
func TestSQLiteMigration_S3PrefixReset(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/prefix.db",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	m := newSQLiteMigrator(t, sqlDB)
	if err := m.Migrate(33); err != nil {
		t.Fatalf("Migrate(33): %v", err)
	}

	rows := map[string]struct{ typ, config, want string }{
		"s3 with prefix":  {"s3", `{"bucket":"b","endpoint":"e","prefix":"backups/","region":"r"}`, `{"bucket":"b","endpoint":"e","region":"r"}`},
		"s3 blank prefix": {"s3", `{"bucket":"b","endpoint":"e","prefix":""}`, `{"bucket":"b","endpoint":"e"}`},
		"s3 no prefix":    {"s3", `{"bucket":"b","endpoint":"e"}`, `{"bucket":"b","endpoint":"e"}`},
		"s3 invalid json": {"s3", `not json "prefix"`, `not json "prefix"`},
		"local not s3":    {"local", `{"path":"/r","prefix":"keep"}`, `{"path":"/r","prefix":"keep"}`},
	}
	ids := map[string]uuid.UUID{}
	for name, r := range rows {
		id := uuid.New()
		ids[name] = id
		if err := gdb.Exec(`INSERT INTO destinations (id, name, type, config, credentials, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, '', 1, ?, ?)`,
			id.String(), name, r.typ, r.config, time.Now(), time.Now()).Error; err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Up: %v", err)
	}

	for name, r := range rows {
		var config string
		if err := gdb.Raw(`SELECT config FROM destinations WHERE id = ?`, ids[name].String()).Scan(&config).Error; err != nil {
			t.Fatalf("query %s: %v", name, err)
		}
		if config != r.want {
			t.Errorf("%s: config = %s, want %s", name, config, r.want)
		}
	}
}
