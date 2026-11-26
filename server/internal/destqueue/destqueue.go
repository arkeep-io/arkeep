// Package destqueue dispatches jobs queued behind a busy destination
// (issue #285).
//
// Every job that runs against a destination's repository holds a lock on it
// (destination_locks, issues #130, #319): backups hold it shared and run side
// by side, a retention sweep or integrity check holds it exclusive. A job that
// finds one of its destinations held in a conflicting mode is not skipped: it
// moves to status "waiting" and this package starts it as soon as all of its
// destinations can be claimed.
//
// The queue is the set of waiting jobs, ordered by creation time, and it is
// FIFO per destination: a job may not claim a destination an older waiting job
// is still waiting for in a conflicting mode, so a job needing several
// destinations — or a retention sweep behind a stream of backups — is never
// starved by younger jobs. A job claims all of its destinations in one
// transaction or none of them, so two jobs can never each hold what the other
// needs.
//
// The queue is drained when something may have freed a destination (a
// destination result, a job ending, a cancel, an agent reconnecting — see
// Notify) and on a fallback ticker that covers every other release path.
package destqueue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/healthcheck"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// KeyTimeoutMinutes is the settings key holding how long, in minutes, a job may
// wait to start before it is failed: in the queue for its destinations, or
// pending for its agent (issue #325). 0 disables the timeout.
const KeyTimeoutMinutes = "jobs.queue.timeout_minutes"

// DefaultTimeoutMinutes applies while no administrator has saved a value: a
// day is longer than any reasonable backup, short enough that a destination
// stuck busy is reported the next day rather than never.
const DefaultTimeoutMinutes = 24 * 60

// MaxTimeoutMinutes caps the configurable timeout at 30 days.
const MaxTimeoutMinutes = 30 * 24 * 60

// TimeoutError is the error recorded on a job that waited too long.
const TimeoutError = "timed out waiting for destination: another operation on the repository kept it busy"

// PendingTimeoutError is the error recorded on a job still pending after the
// timeout: its agent was offline the whole time, or never started it.
const PendingTimeoutError = "timed out waiting for the agent to start the job: it was offline or never picked it up"

// fallbackInterval is how often the queue is drained without a Notify. It only
// matters for releases nothing notifies about (agent watchdog, server restart,
// stale gates), and for the timeout.
const fallbackInterval = 30 * time.Second

// drainLimit bounds how many waiting jobs a single drain looks at.
const drainLimit = 500

// ErrNotAdmitted is returned by a start function when the admit callback
// refused the job: at least one destination is held in a conflicting mode, or
// an older job is waiting for it. Nothing was sent and no lock is held.
var ErrNotAdmitted = errors.New("destinations busy: job must wait")

// AdmitFunc is handed to a start function to claim the destinations a job is
// about to run against. It returns false when the job must wait instead; on
// true, the job holds every destination's lock.
type AdmitFunc func(ctx context.Context, destinationIDs []uuid.UUID) (bool, error)

// Starter rebuilds and sends a queued job of one type. It resolves the
// destinations the job still has to run against (closing out the ones that no
// longer exist), claims them through admit, and dispatches the job to its
// agent.
//
// StartQueued returns ErrNotAdmitted when admit refused. After a successful
// admit, a dispatch failure must release the gates it was given and return the
// error: the dispatcher puts the job back in the queue. A job that can no
// longer run at all (policy or destination deleted) is closed out by the
// starter, which then returns nil.
type Starter interface {
	StartQueued(ctx context.Context, job *db.Job, admit AdmitFunc) error
}

// AgentChecker reports whether an agent has a live stream, and tells it to drop
// a job (implemented by agentmanager.Manager).
type AgentChecker interface {
	IsConnected(agentID string) bool
	SendCancel(agentID, jobID string) error
}

// Dispatcher owns the destination queue. The zero value is not usable —
// create instances with New.
type Dispatcher struct {
	jobs     repositories.JobRepository
	dests    repositories.DestinationRepository
	settings repositories.SettingsRepository
	agents   AgentChecker
	logger   *zap.Logger

	notifSvc notification.Service // may be nil
	pinger   *healthcheck.Pinger  // may be nil
	hub      *websocket.Hub       // may be nil

	startersMu sync.RWMutex
	starters   map[string]Starter

	// drainMu serialises drains: two concurrent drains would each build their
	// own FIFO view and could start jobs out of order.
	drainMu sync.Mutex
	wake    chan struct{}
}

// New creates a Dispatcher. Register a Starter per job type, then call Run.
func New(
	jobs repositories.JobRepository,
	dests repositories.DestinationRepository,
	settings repositories.SettingsRepository,
	agents AgentChecker,
	logger *zap.Logger,
) *Dispatcher {
	return &Dispatcher{
		jobs:     jobs,
		dests:    dests,
		settings: settings,
		agents:   agents,
		logger:   logger.Named("destqueue"),
		starters: make(map[string]Starter),
		wake:     make(chan struct{}, 1),
	}
}

// SetNotificationService attaches the notification service used to report a
// queue timeout. Safe to skip.
func (d *Dispatcher) SetNotificationService(svc notification.Service) {
	d.notifSvc = svc
}

// SetPinger attaches the Healthchecks pinger used to report a queue timeout.
// Safe to skip.
func (d *Dispatcher) SetPinger(p *healthcheck.Pinger) {
	d.pinger = p
}

// SetHub attaches the WebSocket hub, so an open job page follows the job in
// and out of the queue. Safe to skip.
func (d *Dispatcher) SetHub(h *websocket.Hub) {
	d.hub = h
}

// publish pushes a queue-driven status change to the job's WebSocket topic.
func (d *Dispatcher) publish(jobID uuid.UUID, status string, finishedAt *time.Time) {
	if d.hub == nil {
		return
	}
	payload := map[string]any{"job_id": jobID.String(), "status": status}
	if finishedAt != nil {
		payload["finished_at"] = finishedAt.Format(time.RFC3339)
	}
	d.hub.Publish("job:"+jobID.String(), websocket.Message{Type: websocket.MsgJobStatus, Payload: payload})
}

// RegisterStarter sets the Starter for a job type ("backup", "retention").
// Waiting jobs of a type without a Starter are left in the queue.
func (d *Dispatcher) RegisterStarter(jobType string, s Starter) {
	d.startersMu.Lock()
	defer d.startersMu.Unlock()
	d.starters[jobType] = s
}

// Notify asks for a drain soon. Never blocks: notifications that arrive while
// one is already pending collapse into it. Safe to call on a nil Dispatcher.
func (d *Dispatcher) Notify() {
	if d == nil {
		return
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run drains the queue on every Notify and on a fallback ticker, until ctx is
// done. Call once, in its own goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(fallbackInterval)
	defer ticker.Stop()

	d.Drain(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
			d.Drain(ctx)
		case <-ticker.C:
			d.Drain(ctx)
		}
	}
}

// Drain fails the pending and waiting jobs that exceeded the queue timeout,
// then starts every waiting job whose destinations can all be claimed, oldest
// first.
func (d *Dispatcher) Drain(ctx context.Context) {
	d.drainMu.Lock()
	defer d.drainMu.Unlock()

	// A maintenance a repair started ends with the repair (ADR 0026): the
	// admission rule already ignores it, this keeps the destination's columns
	// in line. Drains run on every terminal job status, so it happens promptly.
	if _, err := d.dests.ClearEndedMaintenance(ctx); err != nil {
		d.logger.Warn("failed to clear ended maintenance", zap.Error(err))
	}

	timeout := d.timeout(ctx)
	if timeout > 0 {
		// Before listing the queue: a failed pending job frees its destinations.
		d.failStalePending(ctx, timeout)
	}

	waiting, err := d.jobs.ListWaiting(ctx, drainLimit)
	if err != nil {
		d.logger.Error("failed to list waiting jobs", zap.Error(err))
		return
	}
	if len(waiting) == 0 {
		return
	}

	now := time.Now().UTC()

	// blocked maps the destinations an older waiting job is still waiting for
	// to the mode it wants them in: younger jobs must not take them in a
	// conflicting mode, whether or not they are free.
	blocked := make(map[uuid.UUID]string)

	for i := range waiting {
		job := &waiting[i]

		connected := d.agents.IsConnected(job.AgentID.String())

		if timeout > 0 && now.Sub(job.UpdatedAt) >= timeout {
			// A job of an offline agent was held back by the agent, not by its
			// destinations: it is failed as never started, and not notified,
			// like a stale pending job (issue #345).
			if connected {
				d.failTimedOut(ctx, job, TimeoutError, true)
			} else {
				d.failTimedOut(ctx, job, PendingTimeoutError, false)
			}
			continue
		}

		// An offline agent cannot take the job anyway. It does not reserve its
		// destinations either: holding up everyone else for an agent that may
		// stay away for days would defeat the queue. It is started once the
		// agent reconnects (the gRPC server notifies the queue then).
		if !connected {
			continue
		}

		d.startersMu.RLock()
		starter := d.starters[job.Type]
		d.startersMu.RUnlock()
		if starter == nil {
			continue
		}

		mode := db.LockModeFor(job.Type)
		admitted := false
		admit := func(ctx context.Context, destIDs []uuid.UUID) (bool, error) {
			if conflicts(blocked, destIDs, mode) {
				block(blocked, destIDs, mode)
				return false, nil
			}
			ok, err := d.dests.TryAcquireBusyAll(ctx, destIDs, job.ID, mode)
			if err != nil {
				return false, err
			}
			if !ok {
				block(blocked, destIDs, mode)
				return false, nil
			}
			// Back to pending: the job is being handed to its agent, and from
			// here on it follows the normal pending → running path.
			if err := d.jobs.UpdateStatus(ctx, job.ID, "pending", nil, nil, ""); err != nil {
				// Cancelled meanwhile: let go of what was just claimed.
				d.release(ctx, job.ID, destIDs)
				return false, err
			}
			admitted = true
			d.publish(job.ID, "pending", nil)
			return true, nil
		}

		err := starter.StartQueued(ctx, job, admit)
		switch {
		case err == nil:
			if admitted {
				d.logger.Info("queued job started",
					zap.String("job_id", job.ID.String()),
					zap.String("type", job.Type),
				)
			}
		case errors.Is(err, ErrNotAdmitted):
			// Still waiting.
		case errors.Is(err, repositories.ErrTerminalState):
			// Cancelled while being admitted; nothing left to do.
		default:
			d.logger.Warn("failed to start queued job",
				zap.String("job_id", job.ID.String()),
				zap.String("type", job.Type),
				zap.Error(err),
			)
			if admitted {
				// The starter released the gates; put the job back in line.
				err := d.jobs.UpdateStatus(ctx, job.ID, "waiting", nil, nil, "")
				switch {
				case err == nil:
					d.publish(job.ID, "waiting", nil)
				case !errors.Is(err, repositories.ErrTerminalState):
					d.logger.Error("failed to requeue job after a dispatch failure",
						zap.String("job_id", job.ID.String()),
						zap.Error(err),
					)
				}
			}
		}
	}
}

// conflicts reports whether a job claiming ids in mode would overtake an older
// waiting job: an exclusive claim conflicts with any waiter, a shared claim
// only with an exclusive one.
func conflicts(blocked map[uuid.UUID]string, ids []uuid.UUID, mode string) bool {
	for _, id := range ids {
		if waiting, ok := blocked[id]; ok && (mode == db.LockExclusive || waiting == db.LockExclusive) {
			return true
		}
	}
	return false
}

// block records that a job waits for ids in mode. An exclusive waiter is never
// downgraded by a younger shared one.
func block(blocked map[uuid.UUID]string, ids []uuid.UUID, mode string) {
	for _, id := range ids {
		if blocked[id] != db.LockExclusive {
			blocked[id] = mode
		}
	}
}

func (d *Dispatcher) release(ctx context.Context, jobID uuid.UUID, destIDs []uuid.UUID) {
	for _, id := range destIDs {
		if err := d.dests.ReleaseBusy(ctx, id, jobID); err != nil {
			d.logger.Error("failed to release destination busy gate",
				zap.String("job_id", jobID.String()),
				zap.String("destination_id", id.String()),
				zap.Error(err),
			)
		}
	}
}

// timeout reads the configured queue timeout. A missing or invalid value
// falls back to DefaultTimeoutMinutes; 0 disables the timeout.
func (d *Dispatcher) timeout(ctx context.Context) time.Duration {
	return time.Duration(TimeoutMinutes(ctx, d.settings)) * time.Minute
}

// TimeoutMinutes returns the configured queue timeout in minutes:
// DefaultTimeoutMinutes when unset or unreadable, 0 when disabled.
func TimeoutMinutes(ctx context.Context, settings repositories.SettingsRepository) int {
	if settings == nil {
		return DefaultTimeoutMinutes
	}
	st, err := settings.Get(ctx, KeyTimeoutMinutes)
	if err != nil {
		return DefaultTimeoutMinutes
	}
	n, err := strconv.Atoi(string(st.Value))
	if err != nil || n < 0 {
		return DefaultTimeoutMinutes
	}
	return n
}

// failStalePending fails the jobs still pending after timeout (issue #325): the
// agent was offline the whole time, or received the job and never started it.
// Left pending, such a job silently held back every later run of its policy.
// An agent that is connected is told to drop the job, in case it still sits
// in its local queue. A job of an agent that is offline is not notified as
// failed (issue #345): the agent offline notification already reported it, and
// a powered-off machine is not a backup failure. Healthchecks is still pinged,
// since the backup did not happen. Releasing their destinations may unblock waiting jobs,
// which the drain that called this starts.
func (d *Dispatcher) failStalePending(ctx context.Context, timeout time.Duration) {
	stale, err := d.jobs.ListPendingBefore(ctx, time.Now().UTC().Add(-timeout), drainLimit)
	if err != nil {
		d.logger.Error("failed to list stale pending jobs", zap.Error(err))
		return
	}
	for i := range stale {
		job := &stale[i]
		connected := d.agents.IsConnected(job.AgentID.String())
		if !d.failTimedOut(ctx, job, PendingTimeoutError, connected) {
			continue
		}
		if !connected {
			continue
		}
		if err := d.agents.SendCancel(job.AgentID.String(), job.ID.String()); err != nil {
			d.logger.Warn("failed to cancel a timed-out job on its agent",
				zap.String("job_id", job.ID.String()),
				zap.Error(err),
			)
		}
	}
}

// failTimedOut closes a job that waited longer than the queue timeout with
// errMsg, and reports it like any other failed backup: Healthchecks /fail ping
// and, when notify is set, job failed notification. It reports whether the job
// was failed: false when it had already started or ended meanwhile.
func (d *Dispatcher) failTimedOut(ctx context.Context, job *db.Job, errMsg string, notify bool) bool {
	now := time.Now().UTC()
	if err := d.jobs.UpdateStatus(ctx, job.ID, "failed", nil, &now, errMsg); err != nil {
		if !errors.Is(err, repositories.ErrTerminalState) {
			d.logger.Error("failed to fail a timed-out job",
				zap.String("job_id", job.ID.String()),
				zap.Error(err),
			)
		}
		return false
	}
	d.logger.Warn("job timed out before it started",
		zap.String("job_id", job.ID.String()),
		zap.String("type", job.Type),
		zap.String("status", job.Status),
		zap.Time("since", job.UpdatedAt),
	)
	d.publish(job.ID, "failed", &now)

	if d.pinger != nil {
		go d.pinger.ReportJob(context.WithoutCancel(ctx), job.ID, "failed", errMsg)
	}
	if notify && d.notifSvc != nil {
		go d.notifyFailed(context.WithoutCancel(ctx), job.ID, errMsg)
	}
	return true
}

func (d *Dispatcher) notifyFailed(ctx context.Context, jobID uuid.UUID, errMsg string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	job, dests, _, _, _, err := d.jobs.GetByIDWithDetails(ctx, jobID)
	if err != nil {
		d.logger.Warn("failed to load job for queue timeout notification",
			zap.String("job_id", jobID.String()),
			zap.Error(err),
		)
		return
	}
	if err := d.notifSvc.NotifyJobFailed(ctx, notification.JobSubjectFrom(job, dests), errMsg); err != nil {
		d.logger.Warn("failed to send queue timeout notification",
			zap.String("job_id", jobID.String()),
			zap.Error(err),
		)
	}
}

// Enqueue moves a job that could not be admitted to "waiting" and asks for a
// drain, so it starts as soon as its destinations free up. Called by the
// schedulers when a fresh job finds a destination busy.
//
// Any lock the job still holds is released first: a pending job re-sent to a
// reconnected agent may hold the locks of its earlier dispatch, and a waiting
// job holding some destinations while queued behind others is exactly the
// deadlock the all-or-nothing claim exists to prevent.
func (d *Dispatcher) Enqueue(ctx context.Context, jobID uuid.UUID) error {
	if err := d.dests.ReleaseBusyForJobs(ctx, []uuid.UUID{jobID}); err != nil {
		return fmt.Errorf("failed to release gates of job %s before queueing it: %w", jobID, err)
	}
	if err := d.jobs.UpdateStatus(ctx, jobID, "waiting", nil, nil, ""); err != nil {
		return fmt.Errorf("failed to queue job %s: %w", jobID, err)
	}
	d.logger.Info("job queued: destination busy", zap.String("job_id", jobID.String()))
	d.publish(jobID, "waiting", nil)
	d.Notify()
	return nil
}

// AdmitNew is the admit step for a job of jobType that is not in the queue
// yet: it must not overtake a waiting job that needs one of the same
// destinations in a conflicting mode, and it claims every destination at once
// or none.
func (d *Dispatcher) AdmitNew(jobID uuid.UUID, jobType string) AdmitFunc {
	mode := db.LockModeFor(jobType)
	return func(ctx context.Context, destIDs []uuid.UUID) (bool, error) {
		queued, err := d.jobs.HasWaitingForDestinations(ctx, destIDs, jobID, mode)
		if err != nil {
			return false, err
		}
		if queued {
			return false, nil
		}
		return d.dests.TryAcquireBusyAll(ctx, destIDs, jobID, mode)
	}
}
