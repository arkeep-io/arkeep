package db

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// base contains the common fields shared by all models.
// ID uses UUID v7 (time-ordered) for efficient B-tree indexing and natural
// chronological ordering without a separate created_at sort. CreatedAt and
// UpdatedAt are managed automatically by GORM.
type Base struct {
	ID        uuid.UUID `gorm:"type:text;primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// BeforeCreate generates a new UUID v7 if the ID is not already set.
// This ensures every record has a valid time-ordered ID before insertion.
func (b *Base) BeforeCreate(tx *gorm.DB) error {
	if b.ID == (uuid.UUID{}) {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		b.ID = id
	}
	return nil
}

// softDelete extends base with a nullable DeletedAt field for soft deletion.
// GORM automatically filters out soft-deleted records from all queries unless
// Unscoped() is used explicitly.
type SoftDelete struct {
	Base
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// -----------------------------------------------------------------------------
// Users & Auth
// -----------------------------------------------------------------------------

// User represents a local or OIDC-authenticated user.
// Password is only set for local accounts — OIDC users authenticate via the
// provider and have an empty Password field.
type User struct {
	Base
	Email        string          `gorm:"uniqueIndex;not null"`
	Password     EncryptedString `gorm:"type:text"` // empty for OIDC users
	DisplayName  string          `gorm:"not null"`
	Role         string          `gorm:"not null;default:'user'"` // "admin" or "user"
	IsActive     bool            `gorm:"not null;default:true"`   // false = account disabled
	OIDCProvider string `gorm:"column:oidc_provider;default:''"` // provider ID if OIDC user
    OIDCSub      string `gorm:"column:oidc_sub;default:''"` // subject claim from OIDC token
	LastLoginAt  *time.Time
	// TOTPSecret is the base32 TOTP shared secret, encrypted at rest. Empty
	// means no secret. A non-empty secret with TwoFactorEnabled false is a
	// pending enrollment that was never confirmed.
	TOTPSecret       EncryptedString `gorm:"type:text"`
	TwoFactorEnabled bool            `gorm:"not null;default:false"`
}

// RefreshToken stores a hashed refresh token associated with a user session.
// The raw token is never stored — only its SHA-256 hash. Tokens are rotated
// on every use and expire after 7 days.
type RefreshToken struct {
	Base
	UserID    uuid.UUID `gorm:"type:text;not null;index"`
	TokenHash string    `gorm:"not null;uniqueIndex"` // SHA-256 hex of the raw token
	ExpiresAt time.Time `gorm:"not null;index"`
	RevokedAt *time.Time
	UserAgent string
	IPAddress string
}

// PasswordResetToken stores a hashed, single-use token for the self-service
// password reset flow. Only local accounts can reset their password — OIDC
// users are managed by their identity provider. The raw token is never stored:
// only its SHA-256 hash. Tokens expire after a short window and are consumed
// (UsedAt set) on the first successful reset.
type PasswordResetToken struct {
	Base
	UserID    uuid.UUID  `gorm:"type:text;not null;index"`
	TokenHash string     `gorm:"not null;index"` // SHA-256 hex of the raw token
	ExpiresAt time.Time  `gorm:"not null"`
	UsedAt    *time.Time // nil = not yet used
}

// TwoFactorChallenge is the short-lived interim state of a two-step login: the
// password has been verified but the TOTP code has not. Only the SHA-256 hash
// of the challenge token is stored — the raw token is returned to the client
// once and echoed back on the second step. Attempts counts failed codes; the
// challenge is consumed (UsedAt set) on success or after too many failures.
type TwoFactorChallenge struct {
	Base
	UserID    uuid.UUID  `gorm:"type:text;not null;index"`
	TokenHash string     `gorm:"not null;index"` // SHA-256 hex of the raw token
	Attempts  int        `gorm:"not null;default:0"`
	ExpiresAt time.Time  `gorm:"not null"`
	UsedAt    *time.Time // nil = still usable
}

// RecoveryCode is a single-use fallback credential issued when a user enables
// two-factor authentication. Only the SHA-256 hash of the normalised code is
// stored; the raw codes are shown to the user exactly once.
type RecoveryCode struct {
	Base
	UserID   uuid.UUID  `gorm:"type:text;not null;index"`
	CodeHash string     `gorm:"not null;index"` // SHA-256 hex of the normalised code
	UsedAt   *time.Time // nil = not yet used
}

// OIDCProvider stores the configuration for an external OIDC identity provider.
// ClientSecret is encrypted at rest. Multiple providers are supported.
// The callback URL is computed server-side as {base_url}/api/v1/auth/oidc/callback
// and is not stored in the database.
type OIDCProvider struct {
	Base
	Name         string          `gorm:"not null"`
	Issuer       string          `gorm:"not null"`
	ClientID     string          `gorm:"not null"`
	ClientSecret EncryptedString `gorm:"type:text;not null"`
	Scopes       string          `gorm:"not null;default:'openid email profile'"` // space-separated
	Enabled      bool            `gorm:"not null;default:false"`
}

// TableName overrides GORM's default naming convention, which would produce
// "o_id_c_providers" by splitting each uppercase letter as a word boundary.
func (OIDCProvider) TableName() string { return "oidc_providers" }

// -----------------------------------------------------------------------------
// Agents
// -----------------------------------------------------------------------------

// Agent represents a registered backup agent running on a remote machine.
// Agents connect to the server via a persistent gRPC stream (pull pattern) and
// do not expose any ports. The RegistrationToken is used only during the initial
// handshake and is cleared after successful registration.
type Agent struct {
	SoftDelete
	Name            string     `gorm:"not null"`
	Hostname        string     `gorm:"not null"`
	IPAddress       string     `gorm:"not null;default:''"`
	OS              string     `gorm:"not null;default:''"`
	Arch            string     `gorm:"not null;default:''"`
	Version         string     `gorm:"not null;default:''"`
	Status          string     `gorm:"not null;default:'offline'"` // "online", "offline", "error"
	LastSeenAt      *time.Time
	Labels          string `gorm:"type:text;default:'{}'"` // JSON key-value pairs for filtering
	// DockerAvailable is true when the agent can reach the Docker daemon on its host.
	// Advertised by the agent in the Register RPC via AgentCapabilities.docker.
	// Used by the GUI to show or hide the Docker volume source option in the policy form.
	DockerAvailable bool `gorm:"not null;default:false"`
}

// -----------------------------------------------------------------------------
// Destinations
// -----------------------------------------------------------------------------

// Destination represents a backup storage target. Credentials are encrypted at
// rest via EncryptedString. The Config field holds provider-specific settings
// serialized as JSON (e.g. bucket name, endpoint, region for S3).
type Destination struct {
	SoftDelete
	Name        string          `gorm:"not null"`
	Type        string          `gorm:"not null"` // "local", "s3", "sftp", "rest", "rclone"
	Credentials EncryptedString `gorm:"type:text"` // JSON, encrypted
	Config      string          `gorm:"type:text;default:'{}'"` // JSON, not sensitive
	Enabled     bool            `gorm:"not null;default:true"`
	// RepoSizeBytes is the real deduplicated on-disk size of this destination's
	// restic repository (from `restic stats --mode raw-data`), refreshed after
	// each backup. Zero until the first backup or import completes.
	RepoSizeBytes     int64      `gorm:"not null;default:0"`
	RepoSizeUpdatedAt *time.Time `gorm:""`
	// RepoPassword is the Restic repository password, stored only for
	// destinations whose snapshots were imported from a pre-existing
	// repository. Those snapshots have no policy to take the password from,
	// so browse and restore rely on this field. Empty otherwise.
	RepoPassword EncryptedString `gorm:"type:text"`
}

// -----------------------------------------------------------------------------
// Policies
// -----------------------------------------------------------------------------

// Policy defines what to back up, when, and how. It is associated with one
// agent and one or more destinations via PolicyDestination. The schedule uses
// standard cron expression syntax (e.g. "0 2 * * *" for 2 AM daily).
//
// Association fields are intentionally absent from this struct. GORM cannot
// resolve foreign keys when the primary key is uuid.UUID (a custom type).
// Related records are loaded via explicit queries in the repository layer
// (see repository/policy.go: GetByIDWithDestinations).
type Policy struct {
	SoftDelete
	Name             string          `gorm:"not null"`
	AgentID          uuid.UUID       `gorm:"type:text;not null;index"`
	Schedule         string          `gorm:"not null"` // cron expression
	Enabled          bool            `gorm:"not null;default:true"`
	Sources          string          `gorm:"type:text;not null"` // JSON array of source paths
	RetentionLast    int             `gorm:"not null;default:0"`
	RetentionHourly  int             `gorm:"not null;default:0"`
	RetentionDaily   int             `gorm:"not null;default:0"`
	RetentionWeekly  int             `gorm:"not null;default:0"`
	RetentionMonthly int             `gorm:"not null;default:0"`
	RetentionYearly  int             `gorm:"not null;default:0"`
	RepoPassword     EncryptedString `gorm:"type:text;not null"` // Restic repository password
	HookPreBackup    string          `gorm:"type:text;default:''"` // shell command, optional
	HookPostBackup   string          `gorm:"type:text;default:''"` // shell command, optional
	ExcludePatterns  string          `gorm:"type:text;default:'[]'"` // JSON array of --exclude patterns
	// ResumeInterrupted enables automatic resume of a backup whose agent
	// disconnected mid-run. New policies default to true — restic reuses the
	// packs already uploaded, so resuming only transfers what is missing.
	//
	// No `default` tag on purpose: GORM omits a zero-valued field from the INSERT
	// when it knows the column has a default, which would silently turn an
	// explicit false into true. The DEFAULT TRUE in the migration is what
	// backfills pre-existing rows.
	ResumeInterrupted bool `gorm:"not null"`
	LastRunAt         *time.Time
	NextRunAt         *time.Time

	// Destinations is populated by GetByIDWithDestinations via a manual query.
	// The gorm:"-" tag prevents GORM from attempting foreign key resolution
	// on this field, which would fail with uuid.UUID primary keys.
	Destinations []PolicyDestination `gorm:"-"`
}

// PolicyDestination is the join table between Policy and Destination.
// Priority determines the order in which destinations are tried (lower = first).
// This enables 3-2-1 backup rules with multiple destinations per policy.
type PolicyDestination struct {
	Base
	PolicyID      uuid.UUID `gorm:"type:text;not null;index"`
	DestinationID uuid.UUID `gorm:"type:text;not null;index"`
	Priority      int       `gorm:"not null;default:0"`
}

// -----------------------------------------------------------------------------
// Jobs
// -----------------------------------------------------------------------------

// Job represents a single backup execution triggered by the scheduler or
// manually. Status transitions: pending -> running -> succeeded | failed |
// cancelled | interrupted.
//
// "interrupted" means the agent vanished while the job was running (host shut
// down, sleep, network loss), as opposed to "failed", which is a real error
// reported by the agent. Only interrupted jobs are eligible for automatic
// resume.
//
// Destinations and Logs are populated by GetByIDWithDetails via manual queries.
// The gorm:"-" tag prevents GORM from attempting foreign key resolution on
// these fields, which would fail with uuid.UUID primary keys.
type Job struct {
	Base
	// PolicyID is nil for a restore job started from an imported snapshot:
	// such a snapshot belongs to no policy.
	PolicyID  *uuid.UUID `gorm:"type:text;index"`
	AgentID   uuid.UUID  `gorm:"type:text;not null;index"`
	Type      string     `gorm:"not null;default:'backup'"` // "backup", "restore"
	Status    string     `gorm:"not null;default:'pending'"` // "pending", "running", "succeeded", "failed", "cancelled", "interrupted"
	StartedAt *time.Time
	EndedAt   *time.Time
	Error     string `gorm:"type:text;default:''"` // populated on failure

	// ResumeOfJobID is the interrupted job this run resumes, nil for a normal
	// run. No foreign key: the original job may be removed by job retention.
	ResumeOfJobID *uuid.UUID `gorm:"type:text"`
	// ResumeAttempt counts consecutive resumes, so a host that dies at the same
	// point every time stops being retried. Zero for a normal run.
	ResumeAttempt int `gorm:"not null;default:0"`

	// Populated manually by GetByIDWithDetails — not managed by GORM.
	Destinations []JobDestination `gorm:"-"`
	Logs         []JobLog         `gorm:"-"`
}

// JobDestination tracks the result of a backup job for each individual
// destination. A job can partially succeed if some destinations fail.
type JobDestination struct {
	Base
	JobID         uuid.UUID  `gorm:"type:text;not null;index"`
	DestinationID uuid.UUID  `gorm:"type:text;not null;index"`
	Status        string     `gorm:"not null;default:'pending'"` // mirrors Job.Status
	SnapshotID    string     `gorm:"default:''"` // opaque ID returned by the backup engine
	SizeBytes     int64      `gorm:"default:0"`
	StartedAt     *time.Time
	EndedAt       *time.Time
	Error         string `gorm:"type:text;default:''"`
}

// JobLog stores structured log lines emitted during a job execution.
// Logs are flushed to the database in batches during execution so that
// the GUI can show partial logs even for in-progress jobs.
type JobLog struct {
	Base
	JobID     uuid.UUID `gorm:"type:text;not null;index"`
	Level     string    `gorm:"not null"` // "info", "warn", "error"
	Message   string    `gorm:"type:text;not null"`
	Timestamp time.Time `gorm:"not null;index"`
}

// -----------------------------------------------------------------------------
// Snapshots
// -----------------------------------------------------------------------------

// Snapshot represents a point-in-time backup recorded by the backup engine.
// Snapshots are synced from the engine after each successful job and cached
// in the database for fast listing and filtering without hitting the engine.
type Snapshot struct {
	Base
	// PolicyID and JobID are nil for snapshots imported from a pre-existing
	// repository: they belong to a destination but were not produced by any
	// backup run of any policy.
	PolicyID      *uuid.UUID `gorm:"type:text;index"`
	DestinationID uuid.UUID  `gorm:"type:text;not null;index"`
	JobID         *uuid.UUID `gorm:"type:text;index"`
	SnapshotID    string     `gorm:"not null;index"` // opaque ID from the backup engine
	// SizeBytes is the real footprint this backup added to the repository
	// (restic data_added_packed), not the logical source size — so it reconciles
	// with the destination's real repo size and never double-counts. Zero when
	// the backup added nothing new (e.g. re-backup of unchanged data).
	SizeBytes     int64     `gorm:"default:0"`
	FileCount     int64     `gorm:"default:0"`
	Tags          string    `gorm:"type:text;default:'[]'"`  // JSON array
	Sources       string    `gorm:"type:text;default:'[]'"`  // JSON array of paths backed up
	Hostname      string    `gorm:"not null;default:''"`
	IsImported    bool      `gorm:"not null;default:false"`
	SnapshotAt    time.Time `gorm:"not null;index"`
}

// -----------------------------------------------------------------------------
// Notifications
// -----------------------------------------------------------------------------

// Notification stores in-app notifications delivered to users via WebSocket.
// Read notifications are kept for 30 days and then purged by a background job.
type Notification struct {
	Base
	UserID  uuid.UUID `gorm:"type:text;not null;index"`
	Type    string    `gorm:"not null"` // "job_success", "job_failure", "agent_offline", etc.
	Title   string    `gorm:"not null"`
	Body    string    `gorm:"type:text;not null"`
	ReadAt  *time.Time
	Payload string `gorm:"type:text;default:'{}'"` // JSON, extra context for the frontend
}

// NotificationDelivery tracks the delivery state of a single notification over
// a single external channel (email or webhook). Each Notification can have at
// most one NotificationDelivery row per channel type.
//
// Status transitions:
//
//	pending → sent      (delivery succeeded)
//	pending → pending   (retry scheduled after backoff)
//	pending → exhausted (max 3 retries exceeded, no further attempts)
//
// Rows are automatically removed when the parent Notification is deleted
// (ON DELETE CASCADE on the foreign key).
type NotificationDelivery struct {
	Base
	NotificationID uuid.UUID  `gorm:"type:text;not null;index"`
	Type           string     `gorm:"not null"`          // "email" | "webhook"
	Status         string     `gorm:"not null;default:'pending'"` // "pending" | "sent" | "exhausted"
	Attempts       int        `gorm:"not null;default:0"`
	LastError      string     `gorm:"type:text;not null;default:''"`
	NextRetryAt    *time.Time // nil = ready to process immediately
}

// TableName maps to the migration-created table name.
func (NotificationDelivery) TableName() string { return "notification_delivery_queue" }

// -----------------------------------------------------------------------------
// Audit Log
// -----------------------------------------------------------------------------

// AuditLog records every significant mutation performed via the API:
// who did it, what was changed, when, and from which IP address.
// The table is append-only — records are never updated or deleted.
// user_email is stored denormalized so the log remains readable even if the
// user account is later deleted.
type AuditLog struct {
	ID           uuid.UUID `gorm:"type:text;primaryKey"`
	CreatedAt    time.Time `gorm:"not null"`
	UserID       uuid.UUID `gorm:"type:text;not null;index"`
	UserEmail    string    `gorm:"not null"`
	Action       string    `gorm:"not null;index"` // e.g. "policy.update", "snapshot.restore"
	ResourceType string    `gorm:"not null;default:''"`
	ResourceID   string    `gorm:"type:text;not null;default:''"`
	Details      string    `gorm:"type:text;not null;default:'{}'"` // JSON
	IPAddress    string    `gorm:"not null;default:''"`
}

// BeforeCreate generates a UUID v7 for new audit records (no UpdatedAt — append-only).
func (a *AuditLog) BeforeCreate(tx *gorm.DB) error {
	if a.ID == (uuid.UUID{}) {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		a.ID = id
	}
	return nil
}

// TableName maps to the migration-created table name.
func (AuditLog) TableName() string { return "audit_log" }

// -----------------------------------------------------------------------------
// Settings
// -----------------------------------------------------------------------------

// Setting is a generic key-value configuration entry stored in the database.
// Keys are namespaced by convention (e.g. "smtp.host", "webhook.url").
// Sensitive values (e.g. "smtp.password") are encrypted at the application
// layer via EncryptedString before being persisted.
//
// Setting does not embed base because it uses a string primary key (the key
// itself) rather than a UUID, and does not need CreatedAt.
type Setting struct {
	Key       string          `gorm:"primaryKey"`
	Value     EncryptedString `gorm:"type:text;not null"`
	UpdatedAt time.Time       `gorm:"not null;autoUpdateTime"`
}