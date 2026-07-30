package db

import (
	"bytes"
	"database/sql"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"
)

// TestSQLiteMigrationsDownUp walks every SQLite migration all the way down and
// back up, with an imported snapshot present, so the table rebuilds in the
// 000018 down migration are exercised rather than assumed.
func TestSQLiteMigrationsDownUp(t *testing.T) {
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
