// Package dbtest gives tests a SQLite database with every migration applied.
//
// Running the migrations is the expensive part of opening a test database,
// roughly 2.5 s under the race detector (the modernc driver is SQLite compiled
// to Go, so all of it is instrumented). They run once per test binary on a
// template file; each test then gets its own copy of that file, which opens in
// a few milliseconds and is just as isolated as a fresh in-memory database.
package dbtest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

var (
	templateOnce sync.Once
	template     []byte
	templateErr  error
)

// New returns a database on a private copy of the migrated template. It is
// closed when the test ends.
func New(t testing.TB) *gorm.DB {
	t.Helper()
	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: Path(t), Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("dbtest: open database: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("dbtest: sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}

// Path writes a copy of the migrated template to a temporary directory and
// returns its path, for tests that open the database themselves.
func Path(t testing.TB) string {
	t.Helper()
	templateOnce.Do(buildTemplate)
	if templateErr != nil {
		t.Fatalf("dbtest: build template: %v", templateErr)
	}
	path := filepath.Join(t.TempDir(), "arkeep.db")
	if err := os.WriteFile(path, template, 0o600); err != nil {
		t.Fatalf("dbtest: write database: %v", err)
	}
	return path
}

// buildTemplate migrates a new database file and keeps its bytes.
func buildTemplate() {
	dir, err := os.MkdirTemp("", "arkeep-dbtest-")
	if err != nil {
		templateErr = err
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, "template.db")
	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: path, Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		templateErr = err
		return
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		templateErr = err
		return
	}
	if err := sqlDB.Close(); err != nil {
		templateErr = err
		return
	}
	template, templateErr = os.ReadFile(filepath.Clean(path))
}
