// Package destqueue dispatches jobs queued behind a busy destination
// (issue #285).
//
// A backup or retention sweep needs exclusive use of every destination it runs
// against (the busy gate, destinations.busy_job_id, issue #130). A job that
// finds one of them held is not skipped: it moves to status "waiting" and this
// package starts it as soon as all of its destinations are free.
//
// The queue is the set of waiting jobs, ordered by creation time, and it is
// strictly FIFO per destination: a job may only claim a destination that no
// older waiting job is still waiting for, so a job needing several
// destinations — or a retention sweep — is never starved by a stream of
// younger jobs. A job claims all of its destinations in one transaction or
// none of them, so two jobs can never each hold what the other needs.
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
// wait for its destinations before it is failed. 0 disables the timeout.
const KeyTimeoutMinutes = "jobs.queue.timeout_minutes"

// DefaultTimeoutMinutes applies while no administrator has saved a value: a
// day is longer than any reasonable backup, short enough that a destination
// stuck busy is reported the next day rather than never.
const DefaultTimeoutMinutes = 24 * 60

// MaxTimeoutMinutes caps the configurable timeout at 30 days.
const MaxTimeoutMinutes = 30 * 24 * 60

// TimeoutError is the error recorded on a job that waited too long.
const TimeoutError = "timed out waiting for destination: another backup or retention sweep kept it busy"

// fallbackInterval is how often the queue is drained without a Notify. It only
// matters for releases nothing notifies about (agent watchdog, server restart,
// stale gates), and for the timeout.
const fallbackInterval = 30 * time.Second

// drainLimit bounds how many waiting jobs a single drain looks at.
const drainLimit = 500

// ErrNotAdmitted is returned by a start function when the admit callback
// refused the job: at least one destination is busy, or an older job is
// waiting for it. Nothing was sent and no gate is held.
var ErrNotAdmitted = errors.New("destinations busy: job must wait")

// AdmitFunc is handed to a start function to claim the destinations a job is
// about to run against. It returns false when the job must wait instead; on
// true, every destination's busy gate is held by the job.
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

// AgentChecker reports whether an agent has a live stream.
type AgentChecker interface {
	IsConnected(agentID string) bool
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

// Drain fails the waiting jobs that exceeded the queue timeout, then starts
// every waiting job whose destinations can all be claimed, oldest first.
func (d *Dispatcher) Drain(ctx context.Context) {
	d.drainMu.Lock()
	defer d.drainMu.Unlock()

	waiting, err := d.jobs.ListWaiting(ctx, drainLimit)
	if err != nil {
		d.logger.Error("failed to list waiting jobs", zap.Error(err))
		return
	}
	if len(waiting) == 0 {
		return
	}

	timeout := d.timeout(ctx)
	now := time.Now().UTC()

	// blocked holds the destinations an older waiting job is still waiting
	// for: younger jobs must not take them, whether or not they are free.
	blocked := make(map[uuid.UUID]bool)

	for i := range waiting {
		job := &waiting[i]

		if timeout > 0 && now.Sub(job.UpdatedAt) >= timeout {
			d.failTimedOut(ctx, job)
			continue
		}

		// An offline agent cannot take the job anyway. It does not reserve its
		// destinations either: holding up everyone else for an agent that may
		// stay away for days would defeat the queue. It is started once the
		// agent reconnects (the gRPC server notifies the queue then).
		if !d.agents.IsConnected(job.AgentID.String()) {
			continue
		}

		d.startersMu.RLock()
		starter := d.starters[job.Type]
		d.startersMu.RUnlock()
		if starter == nil {
			continue
		}

		admitted := false
		admit := func(ctx context.Context, destIDs []uuid.UUID) (bool, error) {
			for _, id := range destIDs {
				if blocked[id] {
					block(blocked, destIDs)
					return false, nil
				}
			}
			ok, err := d.dests.TryAcquireBusyAll(ctx, destIDs, job.ID)
			if err != nil {
				return false, err
			}
			if !ok {
				block(blocked, destIDs)
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

func block(blocked map[uuid.UUID]bool, ids []uuid.UUID) {
	for _, id := range ids {
		blocked[id] = true
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

// failTimedOut closes a job that waited longer than the queue timeout, and
// reports it like any other failed backup: notification and Healthchecks
// /fail ping.
func (d *Dispatcher) failTimedOut(ctx context.Context, job *db.Job) {
	now := time.Now().UTC()
	if err := d.jobs.UpdateStatus(ctx, job.ID, "failed", nil, &now, TimeoutError); err != nil {
		if !errors.Is(err, repositories.ErrTerminalState) {
			d.logger.Error("failed to fail a job that timed out in the queue",
				zap.String("job_id", job.ID.String()),
				zap.Error(err),
			)
		}
		return
	}
	d.logger.Warn("job timed out waiting for its destinations",
		zap.String("job_id", job.ID.String()),
		zap.String("type", job.Type),
		zap.Time("waiting_since", job.UpdatedAt),
	)
	d.publish(job.ID, "failed", &now)

	if d.pinger != nil {
		go d.pinger.ReportJob(context.WithoutCancel(ctx), job.ID, "failed", TimeoutError)
	}
	if d.notifSvc != nil {
		go d.notifyFailed(context.WithoutCancel(ctx), job.ID)
	}
}

func (d *Dispatcher) notifyFailed(ctx context.Context, jobID uuid.UUID) {
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
	if err := d.notifSvc.NotifyJobFailed(ctx, notification.JobSubjectFrom(job, dests), TimeoutError); err != nil {
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
// Any gate the job still holds is released first: a pending job re-sent to a
// reconnected agent may hold the gates of its earlier dispatch, and a waiting
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

// AdmitNew is the admit step for a job that is not in the queue yet: it must
// not overtake a waiting job that needs one of the same destinations, and it
// claims every destination at once or none.
func (d *Dispatcher) AdmitNew(jobID uuid.UUID) AdmitFunc {
	return func(ctx context.Context, destIDs []uuid.UUID) (bool, error) {
		queued, err := d.jobs.HasWaitingForDestinations(ctx, destIDs, jobID)
		if err != nil {
			return false, err
		}
		if queued {
			return false, nil
		}
		return d.dests.TryAcquireBusyAll(ctx, destIDs, jobID)
	}
}
