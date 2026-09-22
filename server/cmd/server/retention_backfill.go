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

// retentionBackfillSettingKey guards backfillDestinationRetention so it runs
// exactly once, ever, regardless of how many times the server restarts.
const retentionBackfillSettingKey = "migration.destination_retention_backfilled"

// backfillDestinationRetention is issue #130's one-time migration step:
// retention moved from being a Policy setting to a Destination setting, and
// this populates each Destination's new retention fields from whichever
// Policy used to own that value.
//
// This cannot be a plain .sql migration (see the destination_retention
// migration's comment): it needs EncryptedString decrypt/re-encrypt for
// RepoPassword, which only the Go layer can do, and the legacy
// policies.retention_* columns are no longer mapped by db.Policy (they were
// removed from the struct, not the table — see the migration file for why
// dropping them is deferred to a later release), so they are read here via a
// raw query instead of the model.
//
// For each destination:
//   - exactly one live policy attached: copy that policy's retention values
//     and agent, default the schedule, and enable retention — this is the
//     unambiguous, safe case.
//   - two or more: leave retention disabled and set RetentionNeedsReview, so
//     the GUI can surface it for manual reconfiguration rather than silently
//     picking one policy's values over another's.
//   - zero: leave defaults untouched.
//
// RepoPassword is backfilled separately from the retention values above,
// from the earliest-attached policy (any one is correct — policies sharing a
// destination already point at the same physical repository), whenever the
// destination doesn't already have one (e.g. from a prior "import existing
// repository" flow).
func backfillDestinationRetention(ctx context.Context, gormDB *gorm.DB, destRepo repositories.DestinationRepository, settingsRepo repositories.SettingsRepository, logger *zap.Logger) error {
	if _, err := settingsRepo.Get(ctx, retentionBackfillSettingKey); err == nil {
		return nil // already ran
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return fmt.Errorf("failed to check retention backfill marker: %w", err)
	}

	var destinations []db.Destination
	if err := gormDB.WithContext(ctx).Find(&destinations).Error; err != nil {
		return fmt.Errorf("failed to list destinations for retention backfill: %w", err)
	}

	updated, needReview := 0, 0
	for i := range destinations {
		dest := &destinations[i]

		policies, err := destRepo.ListPoliciesByDestination(ctx, dest.ID)
		if err != nil {
			logger.Warn("retention backfill: failed to list policies for destination",
				zap.String("destination_id", dest.ID.String()),
				zap.Error(err),
			)
			continue
		}

		changed := false
		switch len(policies) {
		case 1:
			p := policies[0]
			var legacy struct {
				RetentionLast    int
				RetentionHourly  int
				RetentionDaily   int
				RetentionWeekly  int
				RetentionMonthly int
				RetentionYearly  int
			}
			err := gormDB.WithContext(ctx).Raw(
				`SELECT retention_last, retention_hourly, retention_daily,
				        retention_weekly, retention_monthly, retention_yearly
				 FROM policies WHERE id = ?`, p.ID,
			).Scan(&legacy).Error
			if err != nil {
				logger.Warn("retention backfill: failed to read legacy policy retention",
					zap.String("policy_id", p.ID.String()),
					zap.Error(err),
				)
				break
			}
			dest.RetentionLast = legacy.RetentionLast
			dest.RetentionHourly = legacy.RetentionHourly
			dest.RetentionDaily = legacy.RetentionDaily
			dest.RetentionWeekly = legacy.RetentionWeekly
			dest.RetentionMonthly = legacy.RetentionMonthly
			dest.RetentionYearly = legacy.RetentionYearly
			agentID := p.AgentID
			dest.RetentionAgentID = &agentID
			dest.RetentionSchedule = "0 2 * * *"
			dest.RetentionEnabled = true
			changed = true
		case 0:
			// Nothing to inherit — leave defaults.
		default:
			dest.RetentionNeedsReview = true
			changed = true
		}

		if dest.RepoPassword == "" && len(policies) > 0 {
			dest.RepoPassword = policies[0].RepoPassword
			changed = true
		}

		if !changed {
			continue
		}
		if err := destRepo.Update(ctx, dest); err != nil {
			logger.Warn("retention backfill: failed to update destination",
				zap.String("destination_id", dest.ID.String()),
				zap.Error(err),
			)
			continue
		}
		updated++
		if dest.RetentionNeedsReview {
			needReview++
		}
	}

	if err := settingsRepo.Set(ctx, retentionBackfillSettingKey, db.EncryptedString("true")); err != nil {
		return fmt.Errorf("failed to record retention backfill completion: %w", err)
	}

	logger.Info("destination retention backfill complete",
		zap.Int("destinations", len(destinations)),
		zap.Int("destinations_updated", updated),
		zap.Int("destinations_needing_review", needReview),
	)
	return nil
}
