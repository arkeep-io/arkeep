package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/arkeep-io/arkeep/server/internal/db"
)

// -----------------------------------------------------------------------------
// Common
// -----------------------------------------------------------------------------

// ListOptions contains common pagination and filtering options for list queries.
type ListOptions struct {
	Limit  int
	Offset int
}

// -----------------------------------------------------------------------------
// UserRepository
// -----------------------------------------------------------------------------

type UserRepository interface {
	Create(ctx context.Context, user *db.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.User, error)
	GetByEmail(ctx context.Context, email string) (*db.User, error)
	GetByOIDC(ctx context.Context, provider, sub string) (*db.User, error)
	Update(ctx context.Context, user *db.User) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, opts ListOptions) ([]db.User, int64, error)
}

// -----------------------------------------------------------------------------
// RefreshTokenRepository
// -----------------------------------------------------------------------------

type RefreshTokenRepository interface {
	Create(ctx context.Context, token *db.RefreshToken) error
	GetByHash(ctx context.Context, hash string) (*db.RefreshToken, error)
	DeleteByHash(ctx context.Context, hash string) error
	Revoke(ctx context.Context, id uuid.UUID) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
	DeleteExpired(ctx context.Context) error
}

// -----------------------------------------------------------------------------
// OIDCProviderRepository
// -----------------------------------------------------------------------------

type OIDCProviderRepository interface {
	Create(ctx context.Context, provider *db.OIDCProvider) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.OIDCProvider, error)
	GetEnabled(ctx context.Context) (*db.OIDCProvider, error)
	Update(ctx context.Context, provider *db.OIDCProvider) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// -----------------------------------------------------------------------------
// AgentRepository
// -----------------------------------------------------------------------------

type AgentRepository interface {
	Create(ctx context.Context, agent *db.Agent) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.Agent, error)
	GetByRegistrationToken(ctx context.Context, token string) (*db.Agent, error)
	GetByHostname(ctx context.Context, hostname string) (*db.Agent, error)
	Update(ctx context.Context, agent *db.Agent) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt time.Time) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, opts ListOptions) ([]db.Agent, int64, error)
}

// -----------------------------------------------------------------------------
// DestinationRepository
// -----------------------------------------------------------------------------

type DestinationRepository interface {
	Create(ctx context.Context, destination *db.Destination) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.Destination, error)
	Update(ctx context.Context, destination *db.Destination) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, opts ListOptions) ([]db.Destination, int64, error)
}

// -----------------------------------------------------------------------------
// PolicyRepository
// -----------------------------------------------------------------------------

type PolicyRepository interface {
	Create(ctx context.Context, policy *db.Policy) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.Policy, error)

	// GetByIDWithDestinations retrieves a policy together with its associated
	// PolicyDestination records. The destinations are returned as a separate
	// slice rather than embedded in the Policy struct, because GORM cannot
	// auto-resolve UUID-typed foreign keys. Callers iterate the slice directly.
	GetByIDWithDestinations(ctx context.Context, id uuid.UUID) (*db.Policy, []db.PolicyDestination, error)

	Update(ctx context.Context, policy *db.Policy) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, opts ListOptions) ([]db.Policy, int64, error)
	ListByAgent(ctx context.Context, agentID uuid.UUID) ([]db.Policy, error)
	ListEnabled(ctx context.Context) ([]db.Policy, error)
	UpdateSchedule(ctx context.Context, id uuid.UUID, lastRunAt, nextRunAt time.Time) error

	// PolicyDestination
	AddDestination(ctx context.Context, pd *db.PolicyDestination) error
	RemoveDestination(ctx context.Context, policyID, destinationID uuid.UUID) error
	UpdateDestinationPriority(ctx context.Context, policyID, destinationID uuid.UUID, priority int) error
}

// -----------------------------------------------------------------------------
// JobRepository
// -----------------------------------------------------------------------------

type JobRepository interface {
	Create(ctx context.Context, job *db.Job) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.Job, error)

	// GetByIDWithDetails retrieves a job together with its JobDestination and
	// JobLog records. All three are returned as separate values to avoid
	// embedding slice associations in the Job struct (see Policy for rationale).
	// Logs are ordered by timestamp ascending.
	GetByIDWithDetails(ctx context.Context, id uuid.UUID) (*db.Job, []db.JobDestination, []db.JobLog, error)

	Update(ctx context.Context, job *db.Job) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, endedAt *time.Time, errMsg string) error
	List(ctx context.Context, opts ListOptions) ([]db.Job, int64, error)
	ListByPolicy(ctx context.Context, policyID uuid.UUID, opts ListOptions) ([]db.Job, int64, error)
	ListByAgent(ctx context.Context, agentID uuid.UUID, opts ListOptions) ([]db.Job, int64, error)

	// JobDestination
	CreateDestination(ctx context.Context, jd *db.JobDestination) error
	ListDestinationsByJob(ctx context.Context, jobID uuid.UUID) ([]db.JobDestination, error)
	UpdateDestinationStatus(ctx context.Context, id uuid.UUID, status string, endedAt *time.Time, snapshotID string, sizeBytes int64, errMsg string) error

	// JobLog
	BulkCreateLogs(ctx context.Context, logs []db.JobLog) error
	GetLogs(ctx context.Context, jobID uuid.UUID) ([]db.JobLog, error)
}

// -----------------------------------------------------------------------------
// SnapshotRepository
// -----------------------------------------------------------------------------

type SnapshotRepository interface {
	Create(ctx context.Context, snapshot *db.Snapshot) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.Snapshot, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, opts ListOptions) ([]db.Snapshot, int64, error)
	ListByPolicy(ctx context.Context, policyID uuid.UUID, opts ListOptions) ([]db.Snapshot, int64, error)
	ListByDestination(ctx context.Context, destinationID uuid.UUID, opts ListOptions) ([]db.Snapshot, int64, error)
	DeleteBySnapshotID(ctx context.Context, snapshotID string) error
}

// -----------------------------------------------------------------------------
// NotificationRepository
// -----------------------------------------------------------------------------

type NotificationRepository interface {
	Create(ctx context.Context, notification *db.Notification) error
	GetByID(ctx context.Context, id uuid.UUID) (*db.Notification, error)
	MarkAsRead(ctx context.Context, id uuid.UUID) error
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	ListByUser(ctx context.Context, userID uuid.UUID, opts ListOptions) ([]db.Notification, int64, error)
	DeleteReadOlderThan(ctx context.Context, t time.Time) error
}