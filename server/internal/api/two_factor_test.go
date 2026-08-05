package api

import (
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestTwoFactorSchema asserts migration 000018 applied. newTestDeps runs every
// migration against a fresh in-memory SQLite database, so a broken migration
// fails here (and in every other api test) rather than at runtime.
func TestTwoFactorSchema(t *testing.T) {
	deps := newTestDeps(t)
	m := deps.gdb.Migrator()

	for _, col := range []string{"totp_secret", "two_factor_enabled"} {
		if !m.HasColumn(&db.User{}, col) {
			t.Errorf("users.%s column is missing", col)
		}
	}

	for _, tbl := range []string{"two_factor_challenges", "recovery_codes"} {
		if !m.HasTable(tbl) {
			t.Errorf("table %s is missing", tbl)
		}
	}
}
