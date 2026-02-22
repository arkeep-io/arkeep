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
type base struct {
	ID        uuid.UUID `gorm:"type:text;primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// BeforeCreate generates a new UUID v7 if the ID is not already set.
// This ensures every record has a valid time-ordered ID before insertion.
func (b *base) BeforeCreate(tx *gorm.DB) error {
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
type softDelete struct {
	base
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// -----------------------------------------------------------------------------
// Users & Auth
// -----------------------------------------------------------------------------

// User represents a local or OIDC-authenticated user.
// Password is only set for local accounts — OIDC users authenticate via the
// provider and have an empty Password field.
type User struct {
	base
	Email        string          `gorm:"uniqueIndex;not null"`
	Password     EncryptedString `gorm:"type:text"` // empty for OIDC users
	DisplayName  string          `gorm:"not null"`
	Role         string          `gorm:"not null;default:'user'"` // "admin" or "user"
	IsActive     bool            `gorm:"not null;default:true"`   // false = account disabled
	OIDCProvider string          `gorm:"default:''"`              // provider ID if OIDC user
	OIDCSub      string          `gorm:"default:''"`              // subject claim from OIDC token
	LastLoginAt  *time.Time
}

// RefreshToken stores a hashed refresh token associated with a user session.
// The raw token is never stored — only its SHA-256 hash. Tokens are rotated
// on every use and expire after 7 days.
type RefreshToken struct {
	base
	UserID    uuid.UUID `gorm:"type:text;not null;index"`
	TokenHash string    `gorm:"not null;uniqueIndex"` // SHA-256 hex of the raw token
	ExpiresAt time.Time `gorm:"not null;index"`
	RevokedAt *time.Time
	UserAgent string
	IPAddress string
}

// OIDCProvider stores the configuration for an external OIDC identity provider.
// ClientSecret is encrypted at rest. Only one provider is supported at a time
// in the open core tier.
type OIDCProvider struct {
	base
	Name         string          `gorm:"not null"`
	Issuer       string          `gorm:"not null"`
	ClientID     string          `gorm:"not null"`
	ClientSecret EncryptedString `gorm:"type:text;not null"`
	RedirectURL  string          `gorm:"not null"`
	Scopes       string          `gorm:"not null;default:'openid email profile'"` // space-separated
	Enabled      bool            `gorm:"not null;default:false"`
}

// -----------------------------------------------------------------------------
// Agents
// -----------------------------------------------------------------------------

// Agent represents a registered backup agent running on a remote machine.
// Agents connect to the server via a persistent gRPC stream (pull pattern) and
// do not expose any ports. The RegistrationToken is used only during the initial
// handshake and is cleared after successful registration.
type Agent struct {
	softDelete
	Name              string     `gorm:"not null"`
	Hostname          string     `gorm:"not null"`
	IPAddress         string     `gorm:"not null;default:''"`
	OS                string     `gorm:"not null;default:''"`
	Arch              string     `gorm:"not null;default:''"`
	Version           string     `gorm:"not null;default:''"`
	Status            string     `gorm:"not null;default:'offline'"` // "online", "offline", "error"
	LastSeenAt        *time.Time
	RegistrationToken string `gorm:"default:''"` // cleared after registration
	Labels            string `gorm:"type:text;default:'{}'"` // JSON key-value pairs for filtering
}

// -----------------------------------------------------------------------------
// Destinations
// -----------------------------------------------------------------------------

// Destination represents a backup storage target. Credentials are encrypted at
// rest via EncryptedString. The Config field holds provider-specific settings
// serialized as JSON (e.g. bucket name, endpoint, region for S3).
type Destination struct {
	base
	Name        string          `gorm:"not null"`
	Type        string          `gorm:"not null"` // "local", "s3", "sftp", "rest", "rclone"
	Credentials EncryptedString `gorm:"type:text"` // JSON, encrypted
	Config      string          `gorm:"type:text;default:'{}'"` // JSON, not sensitive
	Enabled     bool            `gorm:"not null;default:true"`
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
	softDelete
	Name             string          `gorm:"not null"`
	AgentID          uuid.UUID       `gorm:"type:text;not null;index"`
	Schedule         string          `gorm:"not null"` // cron expression
	Enabled          bool            `gorm:"not null;default:true"`
	Sources          string          `gorm:"type:text;not null"` // JSON array of source paths
	RetentionDaily   int             `gorm:"not null;default:7"`
	RetentionWeekly  int             `gorm:"not null;default:4"`
	RetentionMonthly int             `gorm:"not null;default:6"`
	RetentionYearly  int             `gorm:"not null;default:1"`
	RepoPassword     EncryptedString `gorm:"type:text;not null"` // Restic repository password
	HookPreBackup    string          `gorm:"type:text;default:''"` // shell command, optional
	HookPostBackup   string          `gorm:"type:text;default:''"` // shell command, optional
	LastRunAt        *time.Time
	NextRunAt        *time.Time

	// Destinations is populated by GetByIDWithDestinations via a manual query.
	// The gorm:"-" tag prevents GORM from attempting foreign key resolution
	// on this field, which would fail with uuid.UUID primary keys.
	Destinations []PolicyDestination `gorm:"-"`
}

// PolicyDestination is the join table between Policy and Destination.
// Priority determines the order in which destinations are tried (lower = first).
// This enables 3-2-1 backup rules with multiple destinations per policy.
type PolicyDestination struct {
	base
	PolicyID      uuid.UUID `gorm:"type:text;not null;index"`
	DestinationID uuid.UUID `gorm:"type:text;not null;index"`
	Priority      int       `gorm:"not null;default:0"`
}

// -----------------------------------------------------------------------------
// Jobs
// -----------------------------------------------------------------------------

// Job represents a single backup execution triggered by the scheduler or
// manually. Status transitions: pending -> running -> succeeded | failed.
//
// Destinations and Logs are populated by GetByIDWithDetails via manual queries.
// The gorm:"-" tag prevents GORM from attempting foreign key resolution on
// these fields, which would fail with uuid.UUID primary keys.
type Job struct {
	base
	PolicyID  uuid.UUID  `gorm:"type:text;not null;index"`
	AgentID   uuid.UUID  `gorm:"type:text;not null;index"`
	Status    string     `gorm:"not null;default:'pending'"` // "pending", "running", "succeeded", "failed"
	StartedAt *time.Time
	EndedAt   *time.Time
	Error     string `gorm:"type:text;default:''"` // populated on failure

	// Populated manually by GetByIDWithDetails — not managed by GORM.
	Destinations []JobDestination `gorm:"-"`
	Logs         []JobLog         `gorm:"-"`
}

// JobDestination tracks the result of a backup job for each individual
// destination. A job can partially succeed if some destinations fail.
type JobDestination struct {
	base
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
// Logs are inserted in bulk at job completion, not line by line during
// execution, to avoid high-frequency write pressure on the database.
type JobLog struct {
	base
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
	base
	PolicyID      uuid.UUID `gorm:"type:text;not null;index"`
	DestinationID uuid.UUID `gorm:"type:text;not null;index"`
	JobID         uuid.UUID `gorm:"type:text;not null;index"`
	SnapshotID    string    `gorm:"not null;index"` // opaque ID from the backup engine
	SizeBytes     int64     `gorm:"default:0"`
	FileCount     int64     `gorm:"default:0"`
	Tags          string    `gorm:"type:text;default:'[]'"` // JSON array
	SnapshotAt    time.Time `gorm:"not null;index"`
}

// -----------------------------------------------------------------------------
// Notifications
// -----------------------------------------------------------------------------

// Notification stores in-app notifications delivered to users via WebSocket.
// Read notifications are kept for 30 days and then purged by a background job.
type Notification struct {
	base
	UserID  uuid.UUID `gorm:"type:text;not null;index"`
	Type    string    `gorm:"not null"` // "job_success", "job_failure", "agent_offline", etc.
	Title   string    `gorm:"not null"`
	Body    string    `gorm:"type:text;not null"`
	ReadAt  *time.Time
	Payload string `gorm:"type:text;default:'{}'"` // JSON, extra context for the frontend
}

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