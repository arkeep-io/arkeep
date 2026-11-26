package db

import (
	"bytes"
	"context"
	"database/sql"
	"os"
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
	agent := &Agent{Name: "agent", Status: "online"}
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

	agent := &Agent{Name: "agent", Status: "online"}
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

	agent := &Agent{Name: "agent", Status: "online"}
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

	agent := &Agent{Name: "agent", Status: "online"}
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

// TestSQLiteMigration_DestinationLocks steps 000036 down and up with busy
// destinations present (issue #319): the up migration must carry over the
// holds still in effect, in the mode of the holding job, and drop a stale one;
// the down migration keeps the oldest of several holders.
func TestSQLiteMigration_DestinationLocks(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/locks.db",
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
	if err := m.Migrate(35); err != nil {
		t.Fatalf("Migrate(35): %v", err)
	}

	// The restic and rclone version columns of agents only exist from 000046
	// on, repair_available, and jobs.params and requested_by, from 000047 on.
	agent := &Agent{Name: "agent", Status: "online"}
	if err := gdb.Omit("restic_version", "rclone_version", "repair_available").Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	holders := map[string]struct{ jobType, status, wantMode string }{
		"sweep":  {"retention", "running", LockExclusive},
		"backup": {"backup", "pending", LockShared},
		"stale":  {"backup", "succeeded", ""},
	}
	dests := map[string]*Destination{}
	jobs := map[string]*Job{}
	for name, h := range holders {
		d := &Destination{Name: name, Type: "local", Config: `{"path":"/r"}`, Enabled: true}
		if err := gdb.Create(d).Error; err != nil {
			t.Fatalf("create destination: %v", err)
		}
		j := &Job{AgentID: agent.ID, Type: h.jobType, Status: h.status}
		if err := gdb.Omit("params", "requested_by").Create(j).Error; err != nil {
			t.Fatalf("create job: %v", err)
		}
		if err := gdb.Exec(`UPDATE destinations SET busy_job_id = ?, busy_since = ? WHERE id = ?`, j.ID.String(), time.Now().UTC(), d.ID.String()).Error; err != nil {
			t.Fatalf("hold destination: %v", err)
		}
		dests[name], jobs[name] = d, j
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Up: %v", err)
	}
	for name, h := range holders {
		var locks []DestinationLock
		if err := gdb.Where("destination_id = ?", dests[name].ID).Find(&locks).Error; err != nil {
			t.Fatalf("query locks: %v", err)
		}
		switch {
		case h.wantMode == "" && len(locks) != 0:
			t.Errorf("%s: locks = %+v, want none for a finished holder", name, locks)
		case h.wantMode != "" && (len(locks) != 1 || locks[0].JobID != jobs[name].ID || locks[0].Mode != h.wantMode):
			t.Errorf("%s: locks = %+v, want one %s hold by its job", name, locks, h.wantMode)
		}
	}

	// A second backup joins the first; stepping down keeps the oldest holder.
	other := &Job{AgentID: agent.ID, Type: "backup", Status: "running"}
	if err := gdb.Create(other).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := gdb.Create(&DestinationLock{DestinationID: dests["backup"].ID, JobID: other.ID, Mode: LockShared, AcquiredAt: time.Now().UTC().Add(time.Minute)}).Error; err != nil {
		t.Fatalf("create lock: %v", err)
	}
	if err := m.Migrate(35); err != nil {
		t.Fatalf("Migrate(35) after up: %v", err)
	}
	var busyJobID string
	if err := gdb.Raw(`SELECT busy_job_id FROM destinations WHERE id = ?`, dests["backup"].ID.String()).Scan(&busyJobID).Error; err != nil {
		t.Fatalf("query busy_job_id: %v", err)
	}
	if busyJobID != jobs["backup"].ID.String() {
		t.Errorf("busy_job_id after down = %q, want the oldest holder %s", busyJobID, jobs["backup"].ID)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Up again: %v", err)
	}
}

// TestSQLiteMigration_OIDCSessionLimit checks that 000038 gives the open
// sessions of OIDC-linked accounts a deadline 12 hours after the upgrade that
// reads back as a time, and leaves revoked tokens and local accounts without
// one.
func TestSQLiteMigration_OIDCSessionLimit(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/session.db",
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
	if err := m.Migrate(37); err != nil {
		t.Fatalf("Migrate(37): %v", err)
	}

	// Users are inserted with plain SQL, as the schema stands at 000037: the
	// User model has columns added by later migrations.
	now := time.Now()
	oidcUser := &User{Email: "oidc@test.local", OIDCProvider: uuid.NewString()}
	localUser := &User{Email: "local@test.local"}
	for _, u := range []*User{oidcUser, localUser} {
		u.ID = uuid.New()
		if err := gdb.Exec(`INSERT INTO users (id, email, display_name, role, is_active, oidc_provider, oidc_sub, created_at, updated_at) VALUES (?, ?, 'U', 'user', true, ?, ?, ?, ?)`,
			u.ID.String(), u.Email, u.OIDCProvider, u.OIDCProvider, now, now).Error; err != nil {
			t.Fatalf("create user %s: %v", u.Email, err)
		}
	}

	rows := map[string]struct {
		userID       uuid.UUID
		revokedAt    *time.Time
		wantDeadline bool
	}{
		"open oidc session":    {oidcUser.ID, nil, true},
		"revoked oidc session": {oidcUser.ID, &now, false},
		"local session":        {localUser.ID, nil, false},
	}
	ids := map[string]uuid.UUID{}
	for name, r := range rows {
		id := uuid.New()
		ids[name] = id
		if err := gdb.Exec(`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, revoked_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id.String(), r.userID.String(), name, now.Add(7*24*time.Hour), r.revokedAt, now, now).Error; err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	before := time.Now().Truncate(time.Second)
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Up: %v", err)
	}
	after := time.Now()

	for name, r := range rows {
		var token RefreshToken
		if err := gdb.First(&token, "id = ?", ids[name].String()).Error; err != nil {
			t.Fatalf("query %s: %v", name, err)
		}
		got := token.SessionExpiresAt
		switch {
		case !r.wantDeadline && got != nil:
			t.Errorf("%s: session_expires_at = %v, want NULL", name, *got)
		case r.wantDeadline && got == nil:
			t.Errorf("%s: session_expires_at = NULL, want a deadline", name)
		case r.wantDeadline && (got.Before(before.Add(12*time.Hour)) || got.After(after.Add(12*time.Hour))):
			t.Errorf("%s: session_expires_at = %v, want 12 hours after the upgrade", name, *got)
		}
	}
}

// TestSQLiteMigration_DropAgentLabels checks that 000040 drops agents.labels
// with agents present, and that its down migration brings the column back.
func TestSQLiteMigration_DropAgentLabels(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/labels.db",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gdb.Create(&Agent{Name: "agent", Status: "online"}).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	hasLabels := func() bool {
		return gdb.Migrator().HasColumn(&Agent{}, "labels")
	}
	if hasLabels() {
		t.Fatal("agents.labels still exists after the migrations")
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	m := newSQLiteMigrator(t, sqlDB)
	if err := m.Migrate(39); err != nil {
		t.Fatalf("down 000040: %v", err)
	}
	if !hasLabels() {
		t.Error("down migration did not restore agents.labels")
	}
	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if hasLabels() {
		t.Error("agents.labels exists again after Up")
	}
	var n int64
	if err := gdb.Model(&Agent{}).Count(&n).Error; err != nil || n != 1 {
		t.Errorf("agents = %d (%v), want the row kept", n, err)
	}
}

// TestSQLiteMigration_DestinationRepoInitialized checks that 000041 marks the
// repository of an existing destination as initialized when something has
// already succeeded against it (a backup, a command source, a measured size or
// a snapshot record), and leaves untouched destinations and those that only
// ever failed unmarked.
func TestSQLiteMigration_DestinationRepoInitialized(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/repoinit.db",
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
	if err := m.Migrate(40); err != nil {
		t.Fatalf("Migrate(40): %v", err)
	}

	// The restic and rclone version columns of agents only exist from 000046
	// on, repair_available, and jobs.params and requested_by, from 000047 on.
	agent := &Agent{Name: "agent", Status: "online"}
	if err := gdb.Omit("restic_version", "rclone_version", "repair_available").Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	job := &Job{AgentID: agent.ID, Type: "backup", Status: "succeeded"}
	if err := gdb.Omit("params", "requested_by").Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	dests := map[string]*Destination{}
	for _, name := range []string{"backed-up", "command", "sized", "snapshot", "failed", "new"} {
		d := &Destination{Name: name, Type: "local", Config: `{"path":"/r"}`, Enabled: true}
		if err := gdb.Create(d).Error; err != nil {
			t.Fatalf("create destination: %v", err)
		}
		dests[name] = d
	}
	now := time.Now().UTC()
	rows := []any{
		&JobDestination{JobID: job.ID, DestinationID: dests["backed-up"].ID, Status: "succeeded"},
		&JobDestinationCommand{JobID: job.ID, DestinationID: dests["command"].ID, SourceName: "db", Status: "succeeded"},
		&JobDestination{JobID: job.ID, DestinationID: dests["failed"].ID, Status: "failed"},
	}
	for _, row := range rows {
		if err := gdb.Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}
	// The summary columns of snapshots only exist from 000044 on.
	snap := &Snapshot{DestinationID: dests["snapshot"].ID, SnapshotID: "abc", SnapshotAt: now}
	if err := gdb.Omit("backup_start", "backup_end", "files_new", "files_changed", "files_unmodified",
		"total_bytes_processed", "data_added").Create(snap).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if err := gdb.Model(dests["sized"]).Update("repo_size_updated_at", now).Error; err != nil {
		t.Fatalf("set repo size: %v", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("Up: %v", err)
	}
	for name, d := range dests {
		var got Destination
		if err := gdb.First(&got, "id = ?", d.ID).Error; err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		want := name != "failed" && name != "new"
		if (got.RepoInitializedAt != nil) != want {
			t.Errorf("%s: repo_initialized_at = %v, want set = %v", name, got.RepoInitializedAt, want)
		}
	}

	if err := m.Migrate(40); err != nil {
		t.Fatalf("down 000041: %v", err)
	}
	if gdb.Migrator().HasColumn(&Destination{}, "repo_initialized_at") {
		t.Error("down migration kept destinations.repo_initialized_at")
	}
	if err := m.Up(); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}

// TestSQLiteMigration_NotificationProviders checks that 000043 rebuilds the
// delivery queue keeping its rows, that a provider delivery is deleted with
// its provider, and that the down migration drops provider deliveries only.
func TestSQLiteMigration_NotificationProviders(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/providers.db",
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
	if err := m.Migrate(42); err != nil {
		t.Fatalf("Migrate(42): %v", err)
	}

	user := &User{Email: "a@test.local", DisplayName: "A", Role: "admin", IsActive: true}
	if err := gdb.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	n := &Notification{UserID: user.ID, Type: "job_failure", Title: "t", Body: "b", Payload: "{}"}
	if err := gdb.Create(n).Error; err != nil {
		t.Fatalf("create notification: %v", err)
	}
	if err := gdb.Exec(`INSERT INTO notification_delivery_queue (id, notification_id, type, status) VALUES (?, ?, 'webhook', 'pending')`,
		uuid.Must(uuid.NewV7()).String(), n.ID.String()).Error; err != nil {
		t.Fatalf("insert webhook delivery: %v", err)
	}

	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	count := func(where string) int64 {
		var c int64
		if err := gdb.Model(&NotificationDelivery{}).Where(where).Count(&c).Error; err != nil {
			t.Fatalf("count deliveries: %v", err)
		}
		return c
	}
	if c := count("type = 'webhook'"); c != 1 {
		t.Fatalf("webhook deliveries after Up = %d, want 1", c)
	}

	p := &NotificationProvider{Name: "gotify", Type: "gotify", Config: "{}", Secrets: EncryptedString(`{"token":"x"}`), Enabled: true}
	if err := gdb.Create(p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := gdb.Create(&NotificationDelivery{NotificationID: n.ID, Type: "provider", Status: "pending", ProviderID: &p.ID}).Error; err != nil {
		t.Fatalf("create provider delivery: %v", err)
	}
	if err := gdb.Delete(&NotificationProvider{}, "id = ?", p.ID).Error; err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	if c := count("type = 'provider'"); c != 0 {
		t.Errorf("provider deliveries after deleting the provider = %d, want 0", c)
	}

	p2 := &NotificationProvider{Name: "ntfy", Type: "ntfy", Config: "{}", Enabled: true}
	if err := gdb.Create(p2).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := gdb.Create(&NotificationDelivery{NotificationID: n.ID, Type: "provider", Status: "pending", ProviderID: &p2.ID}).Error; err != nil {
		t.Fatalf("create provider delivery: %v", err)
	}
	if err := m.Migrate(42); err != nil {
		t.Fatalf("down 000043: %v", err)
	}
	if c := count("type = 'webhook'"); c != 1 {
		t.Errorf("webhook deliveries after down = %d, want 1", c)
	}
	if gdb.Migrator().HasTable("notification_providers") {
		t.Error("notification_providers still exists after down")
	}
}

// TestSQLiteMigration_SnapshotSummary checks that 000044 adds the summary
// columns to snapshots, leaving an existing record with an unknown summary
// (backup_end NULL, figures zero), and that its down migration drops them.
func TestSQLiteMigration_SnapshotSummary(t *testing.T) {
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb, err := New(Config{
		Driver:   "sqlite",
		DSN:      "file:" + t.TempDir() + "/summary.db",
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
	if err := m.Migrate(43); err != nil {
		t.Fatalf("down 000044: %v", err)
	}
	columns := []string{"backup_start", "backup_end", "files_new", "files_changed", "files_unmodified",
		"total_bytes_processed", "data_added"}
	for _, c := range columns {
		if gdb.Migrator().HasColumn(&Snapshot{}, c) {
			t.Errorf("down migration kept snapshots.%s", c)
		}
	}

	dest := &Destination{Name: "dest", Type: "local", Config: `{"path":"/r"}`, Enabled: true}
	if err := gdb.Create(dest).Error; err != nil {
		t.Fatalf("create destination: %v", err)
	}
	snap := &Snapshot{DestinationID: dest.ID, SnapshotID: "abc", SizeBytes: 10, SnapshotAt: time.Now().UTC()}
	if err := gdb.Omit(columns...).Create(snap).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var got Snapshot
	if err := gdb.First(&got, "id = ?", snap.ID).Error; err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if got.SnapshotSummary != (SnapshotSummary{}) || got.SizeBytes != 10 {
		t.Errorf("existing snapshot after Up: summary %+v, size %d; want an unknown summary and the size kept", got.SnapshotSummary, got.SizeBytes)
	}
}

// TestSQLitePragmasSurviveReconnect checks that foreign_keys and secure_delete
// are on for every connection, not only the first: database/sql replaces a
// connection whose query its context interrupted, and the replacement used to
// run with foreign keys off, so ON DELETE CASCADE stopped working and left
// orphan rows behind (they made 000043 fail on upgrade to v0.12.0).
func TestSQLitePragmasSurviveReconnect(t *testing.T) {
	path := t.TempDir() + "/pragmas.db"
	gdb, err := New(Config{Driver: "sqlite", DSN: path, Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not at the DSN path: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	var n int
	err = gdb.WithContext(ctx).Raw("WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 100000000) SELECT count(*) FROM c").Scan(&n).Error
	if err == nil {
		t.Fatal("the long query was not interrupted")
	}

	for _, pragma := range []string{"foreign_keys", "secure_delete"} {
		var v int
		if err := gdb.Raw("PRAGMA " + pragma).Scan(&v).Error; err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if v != 1 {
			t.Errorf("PRAGMA %s = %d after an interrupted query, want 1", pragma, v)
		}
	}
}

// openAt42 returns a database migrated up to 000042, before the notification
// providers, with one delivery of an existing notification and one orphan
// delivery whose notification is gone, as a database that ran with foreign
// keys off holds.
func openAt42(t *testing.T, path string) (*migrate.Migrate, *sql.DB) {
	t.Helper()
	if err := InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	m := newSQLiteMigrator(t, sqlDB)
	if err := m.Migrate(42); err != nil {
		t.Fatalf("Migrate(42): %v", err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, email, display_name, role, is_active) VALUES ('u1', 'a@test.local', 'A', 'admin', 1)`,
		`INSERT INTO notifications (id, user_id, type, title, body, payload) VALUES ('n1', 'u1', 'job_failure', 't', 'b', '{}')`,
		`INSERT INTO notification_delivery_queue (id, notification_id, type) VALUES ('d-kept', 'n1', 'webhook')`,
		`PRAGMA foreign_keys = OFF`,
		`INSERT INTO notification_delivery_queue (id, notification_id, type) VALUES ('d-orphan', 'gone', 'email')`,
	} {
		if _, err := sqlDB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return m, sqlDB
}

// deliveryIDs lists the ids in notification_delivery_queue.
func deliveryIDs(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT id FROM notification_delivery_queue ORDER BY id`)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestSQLiteMigration_NotificationProvidersOrphanDeliveries checks that 000043
// applies to a queue holding a delivery whose notification is gone, and drops
// that delivery: in v0.12.0 the rebuild ran with foreign keys on and stopped
// at the copy, leaving the database dirty.
func TestSQLiteMigration_NotificationProvidersOrphanDeliveries(t *testing.T) {
	path := t.TempDir() + "/orphans.db"
	m, _ := openAt42(t, path)
	if _, _, err := m.Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}

	gdb, err := New(Config{Driver: "sqlite", DSN: path, Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("New (migrating 000043 onwards): %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if got := deliveryIDs(t, sqlDB); len(got) != 1 || got[0] != "d-kept" {
		t.Errorf("deliveries after 000043 = %v, want [d-kept]", got)
	}
	var violations int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if violations != 0 {
		t.Errorf("foreign key violations after 000043 = %d, want 0", violations)
	}
}

// TestSQLiteRepairsMigration43LeftDirty reproduces the state v0.12.0 left
// behind when 000043 stopped at the copy of the queue (version 43 dirty, the
// providers table and the new queue created, the old queue untouched) and
// checks that opening the database repairs it and applies the migrations.
func TestSQLiteRepairsMigration43LeftDirty(t *testing.T) {
	path := t.TempDir() + "/dirty.db"
	_, raw := openAt42(t, path)
	for _, q := range []string{
		`CREATE TABLE notification_providers (id TEXT NOT NULL PRIMARY KEY)`,
		`CREATE TABLE notification_delivery_queue_new (id TEXT NOT NULL PRIMARY KEY, provider_id TEXT)`,
		`UPDATE schema_migrations SET version = 43, dirty = 1`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = raw.Close()

	gdb, err := New(Config{Driver: "sqlite", DSN: path, Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("New on a database left dirty at 43: %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	var version int
	var dirty bool
	if err := sqlDB.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if version < 44 || dirty {
		t.Errorf("schema_migrations = (%d, dirty %v), want >= 44 and clean", version, dirty)
	}
	if got := deliveryIDs(t, sqlDB); len(got) != 1 || got[0] != "d-kept" {
		t.Errorf("deliveries after the repair = %v, want [d-kept]", got)
	}
	if gdb.Migrator().HasTable("notification_delivery_queue_new") {
		t.Error("notification_delivery_queue_new still exists")
	}
	if !gdb.Migrator().HasColumn(&NotificationDelivery{}, "provider_id") {
		t.Error("notification_delivery_queue has no provider_id: 000043 was not applied")
	}
}
