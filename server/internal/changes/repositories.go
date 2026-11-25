package changes

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// ─── Agents ──────────────────────────────────────────────────────────────────

// agentRepository publishes an "agents" change after each write that changes
// what the GUI shows. Every heartbeat calls UpdateStatus to refresh
// last_seen_at: that is announced only when the status changes, as the last
// status written through this repository tells (an agent's first one after a
// restart of the server is announced too).
type agentRepository struct {
	repositories.AgentRepository
	notifier

	mu     sync.Mutex
	status map[uuid.UUID]string
}

// NewAgentRepository wraps agents so that its writes publish an "agents"
// change on pub.
func NewAgentRepository(agents repositories.AgentRepository, pub Publisher) repositories.AgentRepository {
	return &agentRepository{
		AgentRepository: agents,
		notifier:        newNotifier(pub, websocket.ChangeTypeAgents),
		status:          make(map[uuid.UUID]string),
	}
}

// setStatus records the status of an agent and reports whether it changed.
func (r *agentRepository) setStatus(id uuid.UUID, status string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, known := r.status[id]
	r.status[id] = status
	return !known || previous != status
}

func (r *agentRepository) Create(ctx context.Context, agent *db.Agent) error {
	err := r.AgentRepository.Create(ctx, agent)
	if err == nil {
		r.setStatus(agent.ID, agent.Status)
	}
	return r.after(agent.ID, err)
}

func (r *agentRepository) Update(ctx context.Context, agent *db.Agent) error {
	err := r.AgentRepository.Update(ctx, agent)
	if err == nil {
		r.setStatus(agent.ID, agent.Status)
	}
	return r.after(agent.ID, err)
}

func (r *agentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt time.Time) error {
	err := r.AgentRepository.UpdateStatus(ctx, id, status, lastSeenAt)
	if err == nil && r.setStatus(id, status) {
		r.changed(id)
	}
	return err
}

func (r *agentRepository) MarkOfflineIfStale(ctx context.Context, id uuid.UUID, cutoff time.Time) (bool, error) {
	marked, err := r.AgentRepository.MarkOfflineIfStale(ctx, id, cutoff)
	if err == nil && marked {
		r.setStatus(id, "offline")
	}
	return r.afterIf(id, marked, err)
}

func (r *agentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	err := r.AgentRepository.Delete(ctx, id)
	if err == nil {
		r.mu.Lock()
		delete(r.status, id)
		r.mu.Unlock()
	}
	return r.after(id, err)
}

func (r *agentRepository) BindCertFingerprint(ctx context.Context, id uuid.UUID, fingerprint string) (bool, error) {
	bound, err := r.AgentRepository.BindCertFingerprint(ctx, id, fingerprint)
	return r.afterIf(id, bound, err)
}

func (r *agentRepository) ResetCertFingerprint(ctx context.Context, id uuid.UUID) error {
	return r.after(id, r.AgentRepository.ResetCertFingerprint(ctx, id))
}

// ─── Policies ────────────────────────────────────────────────────────────────

// policyRepository publishes a "policies" change after each successful write.
type policyRepository struct {
	repositories.PolicyRepository
	notifier
}

// NewPolicyRepository wraps policies so that its writes publish a "policies"
// change on pub.
func NewPolicyRepository(policies repositories.PolicyRepository, pub Publisher) repositories.PolicyRepository {
	return &policyRepository{PolicyRepository: policies, notifier: newNotifier(pub, websocket.ChangeTypePolicies)}
}

func (r *policyRepository) Create(ctx context.Context, policy *db.Policy) error {
	err := r.PolicyRepository.Create(ctx, policy)
	return r.after(policy.ID, err)
}

func (r *policyRepository) Update(ctx context.Context, policy *db.Policy) error {
	return r.after(policy.ID, r.PolicyRepository.Update(ctx, policy))
}

func (r *policyRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.after(id, r.PolicyRepository.Delete(ctx, id))
}

func (r *policyRepository) UpdateSchedule(ctx context.Context, id uuid.UUID, lastRunAt, nextRunAt time.Time) error {
	return r.after(id, r.PolicyRepository.UpdateSchedule(ctx, id, lastRunAt, nextRunAt))
}

func (r *policyRepository) UpdateNextRunAt(ctx context.Context, id uuid.UUID, nextRunAt time.Time) error {
	return r.after(id, r.PolicyRepository.UpdateNextRunAt(ctx, id, nextRunAt))
}

func (r *policyRepository) CreateWithDestinations(ctx context.Context, policy *db.Policy, dests []db.PolicyDestination) error {
	err := r.PolicyRepository.CreateWithDestinations(ctx, policy, dests)
	return r.after(policy.ID, err)
}

func (r *policyRepository) UpdateWithDestinations(ctx context.Context, policy *db.Policy, dests []db.PolicyDestination) error {
	return r.after(policy.ID, r.PolicyRepository.UpdateWithDestinations(ctx, policy, dests))
}

func (r *policyRepository) AddDestination(ctx context.Context, pd *db.PolicyDestination) error {
	return r.after(pd.PolicyID, r.PolicyRepository.AddDestination(ctx, pd))
}

func (r *policyRepository) RemoveDestination(ctx context.Context, policyID, destinationID uuid.UUID) error {
	return r.after(policyID, r.PolicyRepository.RemoveDestination(ctx, policyID, destinationID))
}

// DeleteDestinationAssociations changes every policy that used the
// destination.
func (r *policyRepository) DeleteDestinationAssociations(ctx context.Context, destinationID uuid.UUID) error {
	return r.after(uuid.Nil, r.PolicyRepository.DeleteDestinationAssociations(ctx, destinationID))
}

// ─── Destinations ────────────────────────────────────────────────────────────

// destinationRepository publishes a "destinations" change after each
// successful write, the locks and maintenance included: the destination pages
// show them.
type destinationRepository struct {
	repositories.DestinationRepository
	notifier
}

// NewDestinationRepository wraps destinations so that its writes publish a
// "destinations" change on pub.
func NewDestinationRepository(destinations repositories.DestinationRepository, pub Publisher) repositories.DestinationRepository {
	return &destinationRepository{DestinationRepository: destinations, notifier: newNotifier(pub, websocket.ChangeTypeDestinations)}
}

func (r *destinationRepository) Create(ctx context.Context, destination *db.Destination) error {
	err := r.DestinationRepository.Create(ctx, destination)
	return r.after(destination.ID, err)
}

func (r *destinationRepository) Update(ctx context.Context, destination *db.Destination) error {
	return r.after(destination.ID, r.DestinationRepository.Update(ctx, destination))
}

func (r *destinationRepository) UpdateRepoSize(ctx context.Context, id uuid.UUID, sizeBytes int64, at time.Time) error {
	return r.after(id, r.DestinationRepository.UpdateRepoSize(ctx, id, sizeBytes, at))
}

func (r *destinationRepository) MarkRepoInitialized(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.after(id, r.DestinationRepository.MarkRepoInitialized(ctx, id, at))
}

func (r *destinationRepository) ResetRepoInitialized(ctx context.Context, id uuid.UUID) error {
	return r.after(id, r.DestinationRepository.ResetRepoInitialized(ctx, id))
}

func (r *destinationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.after(id, r.DestinationRepository.Delete(ctx, id))
}

func (r *destinationRepository) UpdateLastCheck(ctx context.Context, destinationID, jobID uuid.UUID, status string, at time.Time) error {
	return r.after(destinationID, r.DestinationRepository.UpdateLastCheck(ctx, destinationID, jobID, status, at))
}

func (r *destinationRepository) TryAcquireBusyAll(ctx context.Context, destinationIDs []uuid.UUID, jobID uuid.UUID, mode string) (bool, error) {
	acquired, err := r.DestinationRepository.TryAcquireBusyAll(ctx, destinationIDs, jobID, mode)
	if acquired {
		err = r.afterMany(destinationIDs, err)
	}
	return acquired, err
}

func (r *destinationRepository) ReleaseBusy(ctx context.Context, destinationID, jobID uuid.UUID) error {
	return r.after(destinationID, r.DestinationRepository.ReleaseBusy(ctx, destinationID, jobID))
}

// ReleaseBusyForJobs releases the locks of jobs on any destination.
func (r *destinationRepository) ReleaseBusyForJobs(ctx context.Context, jobIDs []uuid.UUID) error {
	err := r.DestinationRepository.ReleaseBusyForJobs(ctx, jobIDs)
	if err == nil && len(jobIDs) > 0 {
		r.changed(uuid.Nil)
	}
	return err
}

func (r *destinationRepository) ReleaseStaleBusy(ctx context.Context) (int64, error) {
	return r.afterBulk(r.DestinationRepository.ReleaseStaleBusy(ctx))
}

func (r *destinationRepository) StartMaintenance(ctx context.Context, id uuid.UUID, by string) error {
	return r.after(id, r.DestinationRepository.StartMaintenance(ctx, id, by))
}

func (r *destinationRepository) EndMaintenance(ctx context.Context, id uuid.UUID) error {
	return r.after(id, r.DestinationRepository.EndMaintenance(ctx, id))
}

func (r *destinationRepository) ClaimForRepair(ctx context.Context, destinationID, jobID uuid.UUID, by string) (bool, error) {
	auto, err := r.DestinationRepository.ClaimForRepair(ctx, destinationID, jobID, by)
	return auto, r.after(destinationID, err)
}

func (r *destinationRepository) ClearEndedMaintenance(ctx context.Context) (int64, error) {
	return r.afterBulk(r.DestinationRepository.ClearEndedMaintenance(ctx))
}

// ─── Snapshots ───────────────────────────────────────────────────────────────

// snapshotRepository publishes a "snapshots" change after each successful
// write.
type snapshotRepository struct {
	repositories.SnapshotRepository
	notifier
}

// NewSnapshotRepository wraps snapshots so that its writes publish a
// "snapshots" change on pub.
func NewSnapshotRepository(snapshots repositories.SnapshotRepository, pub Publisher) repositories.SnapshotRepository {
	return &snapshotRepository{SnapshotRepository: snapshots, notifier: newNotifier(pub, websocket.ChangeTypeSnapshots)}
}

func (r *snapshotRepository) Create(ctx context.Context, snapshot *db.Snapshot) error {
	err := r.SnapshotRepository.Create(ctx, snapshot)
	return r.after(snapshot.ID, err)
}

func (r *snapshotRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.after(id, r.SnapshotRepository.Delete(ctx, id))
}

func (r *snapshotRepository) DeleteStaleByDestination(ctx context.Context, destinationID uuid.UUID, liveIDs []string, cutoff time.Time) (int64, error) {
	return r.afterBulk(r.SnapshotRepository.DeleteStaleByDestination(ctx, destinationID, liveIDs, cutoff))
}

// FillMissingSummary names the snapshot by its restic ID, not its record's:
// the change carries no ID.
func (r *snapshotRepository) FillMissingSummary(ctx context.Context, destinationID uuid.UUID, snapshotID string, fileCount int64, summary db.SnapshotSummary) (bool, error) {
	filled, err := r.SnapshotRepository.FillMissingSummary(ctx, destinationID, snapshotID, fileCount, summary)
	return r.afterIf(uuid.Nil, filled, err)
}

// ─── Users ───────────────────────────────────────────────────────────────────

// userRepository publishes a "users" change, for admins only, after each
// successful write: logins (last_login_at) and two-factor changes included.
type userRepository struct {
	repositories.UserRepository
	notifier
}

// NewUserRepository wraps users so that its writes publish a "users" change
// on pub.
func NewUserRepository(users repositories.UserRepository, pub Publisher) repositories.UserRepository {
	return &userRepository{UserRepository: users, notifier: newNotifier(pub, websocket.ChangeTypeUsers)}
}

func (r *userRepository) Create(ctx context.Context, user *db.User) error {
	err := r.UserRepository.Create(ctx, user)
	return r.after(user.ID, err)
}

func (r *userRepository) Update(ctx context.Context, user *db.User) error {
	return r.after(user.ID, r.UserRepository.Update(ctx, user))
}

func (r *userRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.after(id, r.UserRepository.Delete(ctx, id))
}

// ─── Audit log ───────────────────────────────────────────────────────────────

// auditRepository publishes an "audit" change, for admins only, after each
// entry written.
type auditRepository struct {
	repositories.AuditRepository
	notifier
}

// NewAuditRepository wraps audit so that its writes publish an "audit" change
// on pub.
func NewAuditRepository(audit repositories.AuditRepository, pub Publisher) repositories.AuditRepository {
	return &auditRepository{AuditRepository: audit, notifier: newNotifier(pub, websocket.ChangeTypeAudit)}
}

func (r *auditRepository) Create(ctx context.Context, entry *db.AuditLog) error {
	err := r.AuditRepository.Create(ctx, entry)
	return r.after(entry.ID, err)
}
