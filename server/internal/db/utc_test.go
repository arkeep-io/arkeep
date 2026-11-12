package db_test

import (
	"bytes"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // America/New_York without system zone data

	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/db/dbtest"
)

// TestTimesStoredInUTC covers R-07: a server outside UTC wrote times with its
// own offset (SQLite) or its wall clock (PostgreSQL), while queries compare
// them with UTC thresholds, so a row written now looked hours old.
func TestTimesStoredInUTC(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	local := time.Local
	time.Local = ny
	t.Cleanup(func() { time.Local = local })

	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb := dbtest.New(t)

	// storedCreatedAt returns created_at of the agent as the database holds it.
	storedCreatedAt := func(t *testing.T, tx *gorm.DB, a *db.Agent) string {
		t.Helper()
		var raw string
		if err := tx.Raw("SELECT created_at FROM agents WHERE id = ?", a.ID).Row().Scan(&raw); err != nil {
			t.Fatalf("read created_at: %v", err)
		}
		return raw
	}

	t.Run("a new row is not older than it is", func(t *testing.T) {
		a := &db.Agent{Name: "a1", Hostname: "h", Status: "offline"}
		if err := gdb.Create(a).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
		if raw := storedCreatedAt(t, gdb, a); !strings.HasSuffix(raw, "Z") {
			t.Errorf("created_at stored as %q, want UTC", raw)
		}
		var n int64
		if err := gdb.Raw("SELECT COUNT(*) FROM agents WHERE id = ? AND created_at < ?", a.ID, time.Now().UTC().Add(-time.Hour)).Scan(&n).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Error("a row created now counts as older than an hour")
		}
	})

	t.Run("inside a transaction", func(t *testing.T) {
		a := &db.Agent{Name: "a2", Hostname: "h", Status: "offline"}
		err := gdb.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(a).Error; err != nil {
				return err
			}
			if raw := storedCreatedAt(t, tx, a); !strings.HasSuffix(raw, "Z") {
				t.Errorf("created_at stored as %q in a transaction, want UTC", raw)
			}
			if _, err := tx.DB(); err != nil {
				t.Errorf("DB() in a transaction: %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("transaction: %v", err)
		}
	})

	t.Run("soft delete", func(t *testing.T) {
		a := &db.Agent{Name: "a3", Hostname: "h", Status: "offline"}
		if err := gdb.Create(a).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := gdb.Delete(a).Error; err != nil {
			t.Fatalf("delete: %v", err)
		}
		var raw string
		if err := gdb.Raw("SELECT deleted_at FROM agents WHERE id = ?", a.ID).Row().Scan(&raw); err != nil {
			t.Fatalf("read deleted_at: %v", err)
		}
		if !strings.HasSuffix(raw, "Z") {
			t.Errorf("deleted_at stored as %q, want UTC", raw)
		}
	})
}
