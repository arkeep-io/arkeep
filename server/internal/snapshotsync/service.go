// Package snapshotsync keeps the snapshot records of a destination in step with
// the repository it points at (issue #288).
//
// The backup and retention jobs already reconcile the records after they run,
// but snapshots can also disappear from outside arkeep — typically an
// append-only rest-server whose owner runs `restic forget --prune` from a cron
// job — or appear there (another restic client writing to the same
// repository). A sync lists the repository through a connected agent
// (JOB_TYPE_IMPORT_SNAPSHOTS, `restic snapshots --no-lock`), records the
// snapshots it does not know yet and evicts the records whose snapshot is gone.
//
// A sync runs on demand (POST /api/v1/destinations/{id}/sync) and, when an
// administrator sets snapshots.sync.interval_hours, periodically for every
// enabled destination.
package snapshotsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destutil"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// KeyIntervalHours is the settings key holding how often, in hours, every
// enabled destination is synced. 0 (the default) disables the periodic sync.
const KeyIntervalHours = "snapshots.sync.interval_hours"

const (
	// tickInterval is how often the periodic loop checks whether a sweep is due.
	tickInterval = time.Hour
	// listPageSize is the page size used to walk every destination in a sweep.
	listPageSize = 100
)

// Sentinel errors returned by SyncDestination.
var (
	// ErrNoAgentAvailable means no connected agent can reach the destination:
	// neither its retention agent nor the agent of any attached policy is online.
	ErrNoAgentAvailable = errors.New("no connected agent can reach this destination")
	// ErrNoRepoPassword means the repository password is unknown: the
	// destination stores none and no attached policy carries one.
	ErrNoRepoPassword = errors.New("repository password unavailable")
	// ErrDestinationBusy means a backup or retention sweep is running against
	// the destination. Syncing then could record that backup's snapshot as an
	// imported one before the backup reports it.
	ErrDestinationBusy = errors.New("destination is busy")
)

// ListingError reports that the agent could not list the repository. Message
// is the raw error from restic.
type ListingError struct {
	Message string
}

func (e *ListingError) Error() string {
	return "repository listing failed: " + e.Message
}

// destinationStore is the subset of the destination repository the service needs.
type destinationStore interface {
	GetByID(ctx context.Context, id uuid.UUID) (*db.Destination, error)
	List(ctx context.Context, opts repositories.ListOptions) ([]db.Destination, int64, error)
	ListPoliciesByDestination(ctx context.Context, destinationID uuid.UUID) ([]db.Policy, error)
	UpdateRepoSize(ctx context.Context, id uuid.UUID, sizeBytes int64, at time.Time) error
}

// snapshotStore is the subset of the snapshot repository the service needs.
type snapshotStore interface {
	Create(ctx context.Context, snapshot *db.Snapshot) error
	ExistsBySnapshotIDAndDestination(ctx context.Context, snapshotID string, destinationID uuid.UUID) (bool, error)
	DeleteStaleByDestination(ctx context.Context, destinationID uuid.UUID, liveIDs []string, cutoff time.Time) (int64, error)
}

// settingsStore is the subset of the settings repository the service needs.
type settingsStore interface {
	Get(ctx context.Context, key string) (*db.Setting, error)
}

// repoLister lists a repository through a connected agent. Satisfied by
// *agentmanager.Manager.
type repoLister interface {
	IsConnected(agentID string) bool
	RequestSnapshotImport(ctx context.Context, agentID, correlationID string, payloadJSON []byte) (agentmanager.SnapshotImportResult, error)
}

// ImportPayload is the JSON payload of a JOB_TYPE_IMPORT_SNAPSHOTS request —
// see the agent's handleSnapshotImportRequest, which this must match.
type ImportPayload struct {
	Type    string            `json:"type"`
	RepoURL string            `json:"repo_url"`
	Env     map[string]string `json:"env"`
}

// NewImportPayload builds the listing payload for a destination, opened with
// the given repository password.
func NewImportPayload(dest *db.Destination, repoPassword string) ImportPayload {
	env := destutil.BuildEnv(dest)
	env["RESTIC_PASSWORD"] = repoPassword
	return ImportPayload{
		Type:    dest.Type,
		RepoURL: destutil.BuildRepoURL(dest),
		Env:     env,
	}
}

// ImportOutcome breaks down what happened to the snapshots the agent reported,
// so a failure to record some of them is never passed off as a clean result.
type ImportOutcome struct {
	// Imported counts snapshots newly recorded.
	Imported int
	// Skipped counts snapshots already recorded for this destination.
	Skipped int
	// Failed counts snapshots that could not be persisted.
	Failed int
}

// Result is the outcome of syncing one destination.
type Result struct {
	// Found is the number of snapshots the repository holds.
	Found int
	ImportOutcome
	// Removed counts records evicted because their snapshot is gone.
	Removed int64
}

// Service syncs snapshot records with their repositories.
type Service struct {
	dests     destinationStore
	snapshots snapshotStore
	settings  settingsStore
	agents    repoLister
	logger    *zap.Logger

	// lastSweep is when the periodic loop last ran a sweep. Only touched by
	// the Start goroutine.
	lastSweep time.Time
}

// NewService creates a snapshot sync Service.
func NewService(dests destinationStore, snapshots snapshotStore, settings settingsStore, agents repoLister, logger *zap.Logger) *Service {
	return &Service{
		dests:     dests,
		snapshots: snapshots,
		settings:  settings,
		agents:    agents,
		logger:    logger.Named("snapshot_sync"),
	}
}

// SyncDestination lists the destination's repository through a connected agent
// and makes its snapshot records match: snapshots the database does not know
// are recorded as imported, records whose snapshot is gone are deleted.
//
// The listing is authoritative, an empty one included: unlike the reconcile
// that follows a backup, a sync has no reason to expect any snapshot, so an
// empty repository evicts every record.
//
// Returns repositories.ErrNotFound for an unknown destination, ErrNoAgentAvailable,
// ErrNoRepoPassword, ErrDestinationBusy, agentmanager.ErrSnapshotImportTimeout
// or a *ListingError.
func (s *Service) SyncDestination(ctx context.Context, destID uuid.UUID) (Result, error) {
	dest, err := s.dests.GetByID(ctx, destID)
	if err != nil {
		return Result{}, err
	}
	if dest.BusyJobID != nil {
		return Result{}, ErrDestinationBusy
	}

	policies, err := s.dests.ListPoliciesByDestination(ctx, destID)
	if err != nil {
		return Result{}, fmt.Errorf("failed to load policies of destination: %w", err)
	}
	agentID := s.pickAgent(dest, policies)
	if agentID == "" {
		return Result{}, ErrNoAgentAvailable
	}
	password := repoPassword(dest, policies)
	if password == "" {
		return Result{}, ErrNoRepoPassword
	}

	payloadBytes, err := json.Marshal(NewImportPayload(dest, password))
	if err != nil {
		return Result{}, fmt.Errorf("failed to marshal listing payload: %w", err)
	}

	// Taken before the listing: a record created after this instant — by a
	// backup that starts while the agent is listing — may be missing from the
	// listing without being stale, so it must survive the eviction.
	cutoff := time.Now().UTC()
	listing, err := s.agents.RequestSnapshotImport(ctx, agentID, uuid.New().String(), payloadBytes)
	if err != nil {
		return Result{}, err
	}
	if listing.Err != "" {
		return Result{}, &ListingError{Message: listing.Err}
	}

	res := Result{
		Found:         len(listing.Snapshots),
		ImportOutcome: s.PersistImported(ctx, dest, &listing),
	}

	liveIDs := make([]string, 0, len(listing.Snapshots))
	for _, info := range listing.Snapshots {
		liveIDs = append(liveIDs, info.ResticSnapshotId)
	}
	res.Removed, err = s.snapshots.DeleteStaleByDestination(ctx, destID, liveIDs, cutoff)
	if err != nil {
		return res, fmt.Errorf("failed to evict stale snapshot records: %w", err)
	}

	s.logger.Info("destination snapshots synced",
		zap.String("destination_id", destID.String()),
		zap.String("agent_id", agentID),
		zap.Int("found", res.Found),
		zap.Int("imported", res.Imported),
		zap.Int64("removed", res.Removed),
		zap.Int("failed", res.Failed),
	)
	return res, nil
}

// pickAgent returns the connected agent the listing runs on: the destination's
// retention agent when it is online, otherwise the first online agent among
// the attached policies. Returns "" when none is connected.
func (s *Service) pickAgent(dest *db.Destination, policies []db.Policy) string {
	if dest.RetentionAgentID != nil && s.agents.IsConnected(dest.RetentionAgentID.String()) {
		return dest.RetentionAgentID.String()
	}
	for _, p := range policies {
		if s.agents.IsConnected(p.AgentID.String()) {
			return p.AgentID.String()
		}
	}
	return ""
}

// repoPassword returns the password that opens the destination's repository:
// the one stored on the destination, otherwise the first one carried by an
// attached policy — every policy writing here shares the same repository.
func repoPassword(dest *db.Destination, policies []db.Policy) string {
	if dest.RepoPassword != "" {
		return string(dest.RepoPassword)
	}
	for _, p := range policies {
		if p.RepoPassword != "" {
			return string(p.RepoPassword)
		}
	}
	return ""
}

// PersistImported records the listed snapshots that are not yet known for this
// destination and caches the repository size the agent reported.
//
// Imported snapshots carry no policy and no job: they were not produced by a
// backup run, so PolicyID and JobID stay nil.
func (s *Service) PersistImported(ctx context.Context, dest *db.Destination, result *agentmanager.SnapshotImportResult) ImportOutcome {
	var out ImportOutcome
	for _, info := range result.Snapshots {
		exists, err := s.snapshots.ExistsBySnapshotIDAndDestination(ctx, info.ResticSnapshotId, dest.ID)
		if err != nil {
			s.logger.Error("failed to check snapshot existence",
				zap.String("restic_snapshot_id", info.ResticSnapshotId),
				zap.Error(err),
			)
			out.Failed++
			continue
		}
		if exists {
			out.Skipped++
			continue
		}

		snapshotAt, err := time.Parse(time.RFC3339Nano, info.SnapshotTime)
		if err != nil {
			snapshotAt, err = time.Parse(time.RFC3339, info.SnapshotTime)
			if err != nil {
				s.logger.Warn("imported snapshot has an unparsable timestamp",
					zap.String("restic_snapshot_id", info.ResticSnapshotId),
					zap.String("snapshot_time", info.SnapshotTime),
					zap.Error(err),
				)
			}
		}
		// Stored in UTC, matching every other SnapshotAt write site — restic
		// reports the local timezone offset, and a non-UTC time.Time round-trips
		// through the SQLite driver as a text format the read-side scan can fail
		// to parse back into time.Time.
		snapshotAt = snapshotAt.UTC()

		sourcesJSON, _ := json.Marshal(info.Paths)
		tagsJSON, _ := json.Marshal(info.Tags)

		snap := &db.Snapshot{
			DestinationID: dest.ID,
			IsImported:    true,
			SnapshotID:    info.ResticSnapshotId,
			Hostname:      info.Hostname,
			Sources:       string(sourcesJSON),
			Tags:          string(tagsJSON),
			SizeBytes:     info.SizeBytes,
			FileCount:     info.FileCount,
			SnapshotAt:    snapshotAt,
		}
		if err := s.snapshots.Create(ctx, snap); err != nil {
			s.logger.Error("failed to create imported snapshot",
				zap.String("restic_snapshot_id", info.ResticSnapshotId),
				zap.Error(err),
			)
			out.Failed++
			continue
		}
		out.Imported++
	}

	// Cache the repo's real size so an imported destination reports accurate
	// usage without waiting for its first scheduled backup. Non-fatal.
	if result.RepoSizeBytes > 0 {
		if err := s.dests.UpdateRepoSize(ctx, dest.ID, result.RepoSizeBytes, time.Now().UTC()); err != nil {
			s.logger.Warn("failed to update destination repo size after import", zap.Error(err))
		}
	}

	return out
}

// Start checks every tickInterval whether a periodic sweep is due and runs it.
// Launch it as a goroutine:
//
//	go svc.Start(ctx)
//
// The interval setting is re-read on every tick, so a change made in the UI
// takes effect without a restart.
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick runs a sweep when the periodic sync is enabled and the configured
// interval has elapsed since the previous one.
func (s *Service) tick(ctx context.Context) {
	hours := s.intervalHours(ctx)
	if hours <= 0 {
		return
	}
	now := time.Now()
	if !s.lastSweep.IsZero() && now.Sub(s.lastSweep) < time.Duration(hours)*time.Hour {
		return
	}
	s.lastSweep = now
	s.SyncAll(ctx)
}

// intervalHours returns the configured sweep interval in hours, 0 when unset,
// unparseable or unreadable.
func (s *Service) intervalHours(ctx context.Context) int {
	setting, err := s.settings.Get(ctx, KeyIntervalHours)
	if err != nil {
		if !errors.Is(err, repositories.ErrNotFound) {
			s.logger.Error("failed to load snapshot sync interval", zap.Error(err))
		}
		return 0
	}
	n, err := strconv.Atoi(string(setting.Value))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// SyncAll syncs every enabled destination, one at a time. A destination that
// cannot be synced right now (no agent online, busy, no password) is skipped
// and retried on the next sweep.
func (s *Service) SyncAll(ctx context.Context) {
	for offset := 0; ; offset += listPageSize {
		dests, total, err := s.dests.List(ctx, repositories.ListOptions{Limit: listPageSize, Offset: offset})
		if err != nil {
			s.logger.Error("failed to list destinations for snapshot sync", zap.Error(err))
			return
		}
		for i := range dests {
			if ctx.Err() != nil {
				return
			}
			if !dests[i].Enabled {
				continue
			}
			s.syncLogged(ctx, dests[i].ID)
		}
		if offset+len(dests) >= int(total) || len(dests) == 0 {
			return
		}
	}
}

// syncLogged syncs one destination for a periodic sweep, logging the outcome
// at a level matching how actionable it is.
func (s *Service) syncLogged(ctx context.Context, destID uuid.UUID) {
	_, err := s.SyncDestination(ctx, destID)
	if err == nil {
		return
	}
	fields := []zap.Field{zap.String("destination_id", destID.String()), zap.Error(err)}
	switch {
	case errors.Is(err, ErrNoAgentAvailable), errors.Is(err, ErrDestinationBusy), errors.Is(err, ErrNoRepoPassword):
		s.logger.Debug("skipping periodic snapshot sync", fields...)
	default:
		s.logger.Warn("periodic snapshot sync failed", fields...)
	}
}
