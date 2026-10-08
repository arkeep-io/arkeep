package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// TestVacuumSecretResidue covers databases upgraded from a release without
// secure_delete: a secret wiped back then is still in the file's free space,
// and the one-time vacuum must erase it.
func TestVacuumSecretResidue(t *testing.T) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	path := filepath.Join(t.TempDir(), "arkeep.db")
	gormDB, err := db.New(db.Config{Driver: "sqlite", DSN: path, Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	ctx := context.Background()
	settingsRepo := repositories.NewSettingsRepository(gormDB)

	// Recreate the old behaviour on the single SQLite connection, then leave
	// residue behind the way a pre-#289-follow-up delete did.
	if err := gormDB.Exec("PRAGMA secure_delete = OFF").Error; err != nil {
		t.Fatalf("disable secure_delete: %v", err)
	}
	residue := "RESIDUE-" + strings.Repeat("ciphertext", 300)
	destRepo := repositories.NewDestinationRepository(gormDB)
	for _, name := range []string{"kept", "deleted"} {
		d := &db.Destination{Name: name, Type: "local", Config: "{}", Enabled: true}
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination: %v", err)
		}
	}
	if err := gormDB.Exec(`UPDATE destinations SET credentials = ? WHERE name = 'deleted'`, residue).Error; err != nil {
		t.Fatalf("store secret: %v", err)
	}
	if err := gormDB.Exec(`UPDATE destinations SET credentials = '' WHERE name = 'deleted'`).Error; err != nil {
		t.Fatalf("wipe secret: %v", err)
	}
	inFile := func() bool {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read database file: %v", err)
		}
		return bytes.Contains(b, []byte(residue[:60]))
	}
	if !inFile() {
		t.Fatal("test setup left no residue in the database file")
	}

	if err := vacuumSecretResidue(ctx, gormDB, settingsRepo, zap.NewNop()); err != nil {
		t.Fatalf("vacuumSecretResidue: %v", err)
	}
	if inFile() {
		t.Error("wiped secret still present in the database file after the vacuum")
	}
	if _, err := settingsRepo.Get(ctx, secretResidueVacuumSettingKey); err != nil {
		t.Errorf("completion marker not recorded: %v", err)
	}

	// Runs once: a second start finds the marker and does not rebuild the
	// file again, so fresh residue is left alone.
	if err := gormDB.Exec(`UPDATE destinations SET credentials = ? WHERE name = 'deleted'`, residue).Error; err != nil {
		t.Fatalf("store secret again: %v", err)
	}
	if err := gormDB.Exec(`UPDATE destinations SET credentials = '' WHERE name = 'deleted'`).Error; err != nil {
		t.Fatalf("wipe secret again: %v", err)
	}
	if err := vacuumSecretResidue(ctx, gormDB, settingsRepo, zap.NewNop()); err != nil {
		t.Fatalf("second vacuumSecretResidue: %v", err)
	}
	if !inFile() {
		t.Error("second start rebuilt the database file again, want the marker to skip it")
	}
}
