// Package logretention provides a background service that prunes old job_logs
// rows so the database does not grow without bound. The job_logs table is
// append-only in normal operation (nothing ever deletes rows), which on
// long-running installs can grow the database to hundreds of megabytes.
//
// Retention is disabled by default: with both settings at 0, no rows are ever
// removed. An administrator opts in via Settings, choosing how many days of
// "info" (and, optionally, "warn"/"error") log lines to keep. Only the verbose
// log lines are affected — the jobs themselves and their outcomes are never
// touched, so the job history is always preserved.
package logretention

import (
	"context"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// Setting keys (stored in the settings key-value table). Both default to 0,
// meaning "keep forever" — retention is opt-in.
const (
	// KeyInfoRetentionDays is the number of days to keep "info" log lines.
	KeyInfoRetentionDays = "logs.retention.info_days"
	// KeyWarnErrorRetentionDays is the number of days to keep "warn"/"error"
	// log lines. Left at 0 by default so important lines are kept indefinitely.
	KeyWarnErrorRetentionDays = "logs.retention.warn_error_days"

	// settingsPrefix is the namespace used to load both keys in one query.
	settingsPrefix = "logs.retention."
)

const (
	// runInterval is how often the retention sweep runs.
	runInterval = 24 * time.Hour
	// batchSize is the number of rows deleted per DELETE statement.
	batchSize = 5000
	// vacuumThreshold is the minimum number of rows a sweep must delete before
	// it reclaims disk space. This avoids rewriting the whole SQLite database
	// file on every daily run when only a handful of rows expired.
	vacuumThreshold = 1000
)

// logStore is the subset of the job repository the service needs.
type logStore interface {
	PruneLogsByLevel(ctx context.Context, levels []string, before time.Time, batchSize int) (int64, error)
	ReclaimLogSpace(ctx context.Context) error
}

// settingsStore is the subset of the settings repository the service needs.
type settingsStore interface {
	GetMany(ctx context.Context, prefix string) ([]db.Setting, error)
}

// Service periodically prunes expired job_logs rows.
type Service struct {
	logs     logStore
	settings settingsStore
	logger   *zap.Logger
}

// NewService creates a log-retention Service.
func NewService(logs logStore, settings settingsStore, logger *zap.Logger) *Service {
	return &Service{
		logs:     logs,
		settings: settings,
		logger:   logger.Named("logretention"),
	}
}

// Start runs an initial sweep, then repeats every runInterval until ctx is
// cancelled. Launch it as a goroutine:
//
//	go svc.Start(ctx)
//
// Settings are re-read on every sweep, so changes made in the UI take effect on
// the next run without a restart.
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(runInterval)
	defer ticker.Stop()

	// Initial sweep shortly after startup so a freshly configured retention
	// window is applied without waiting a full interval. Errors are logged
	// inside RunOnce; a failed sweep must not stop the loop.
	_, _ = s.RunOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single retention sweep and returns the number of rows
// deleted. It is safe to call directly (e.g. from an admin "run now" endpoint).
func (s *Service) RunOnce(ctx context.Context) (int64, error) {
	settings, err := s.settings.GetMany(ctx, settingsPrefix)
	if err != nil {
		s.logger.Error("failed to load retention settings", zap.Error(err))
		return 0, err
	}
	idx := make(map[string]string, len(settings))
	for _, st := range settings {
		idx[st.Key] = string(st.Value)
	}

	infoDays := intSetting(idx, KeyInfoRetentionDays)
	warnErrDays := intSetting(idx, KeyWarnErrorRetentionDays)

	now := time.Now().UTC()
	var total int64

	if infoDays > 0 {
		n, err := s.logs.PruneLogsByLevel(ctx, []string{"info"}, now.AddDate(0, 0, -infoDays), batchSize)
		if err != nil {
			s.logger.Error("failed to prune info logs", zap.Error(err))
			return total, err
		}
		total += n
	}
	if warnErrDays > 0 {
		n, err := s.logs.PruneLogsByLevel(ctx, []string{"warn", "error"}, now.AddDate(0, 0, -warnErrDays), batchSize)
		if err != nil {
			s.logger.Error("failed to prune warn/error logs", zap.Error(err))
			return total, err
		}
		total += n
	}

	if total == 0 {
		return 0, nil
	}

	s.logger.Info("pruned job logs", zap.Int64("deleted", total))

	// Only reclaim disk space after a substantial cleanup to avoid rewriting the
	// whole SQLite file for a handful of expired rows on routine daily runs.
	if total >= vacuumThreshold {
		if err := s.logs.ReclaimLogSpace(ctx); err != nil {
			s.logger.Error("failed to reclaim log space", zap.Error(err))
			return total, err
		}
		s.logger.Info("reclaimed database space after log prune")
	}
	return total, nil
}

// intSetting parses a stored setting as a non-negative int, returning 0 when the
// key is absent or unparseable (0 means "disabled / keep forever").
func intSetting(idx map[string]string, key string) int {
	v, ok := idx[key]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
