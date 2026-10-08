package main

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// secretResidueVacuumSettingKey guards vacuumSecretResidue so it runs once.
const secretResidueVacuumSettingKey = "migration.secret_residue_vacuumed"

// vacuumSecretResidue rebuilds the SQLite database file once, so values
// removed before secure_delete was enabled (issue #289) — such as the secrets
// wiped from deleted destinations and policies — no longer linger in its free
// space. From then on secure_delete zeroes freed content as it happens.
//
// PostgreSQL is left alone: VACUUM there does not erase old row versions from
// disk, WAL or backups, so it would only suggest a guarantee it cannot give.
//
// VACUUM rewrites the whole file and needs about as much free disk space as
// the database itself, so a failure is returned for the caller to log and
// retried on the next start rather than blocking startup.
func vacuumSecretResidue(ctx context.Context, gormDB *gorm.DB, settingsRepo repositories.SettingsRepository, logger *zap.Logger) error {
	if gormDB.Name() != "sqlite" {
		return nil
	}
	if _, err := settingsRepo.Get(ctx, secretResidueVacuumSettingKey); err == nil {
		return nil // already ran
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return fmt.Errorf("failed to check secret residue vacuum marker: %w", err)
	}

	logger.Info("rebuilding the database file to erase removed secrets (one-time, may take a while)")
	if err := gormDB.WithContext(ctx).Exec("VACUUM").Error; err != nil {
		return fmt.Errorf("failed to vacuum database: %w", err)
	}
	if err := settingsRepo.Set(ctx, secretResidueVacuumSettingKey, db.EncryptedString("true")); err != nil {
		return fmt.Errorf("failed to record secret residue vacuum: %w", err)
	}
	logger.Info("database file rebuilt")
	return nil
}
