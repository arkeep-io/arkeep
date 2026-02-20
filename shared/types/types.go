// Package types defines shared domain types used by both server and agent.
package types

import "time"

// ─── Agent ───────────────────────────────────────────────────────────────────

// AgentStatus represents the current connection state of an agent.
type AgentStatus string

const (
	AgentStatusOnline  AgentStatus = "online"
	AgentStatusOffline AgentStatus = "offline"
	AgentStatusError   AgentStatus = "error"
)

// ─── Job ─────────────────────────────────────────────────────────────────────

// JobStatus represents the current execution state of a job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusMissed    JobStatus = "missed"
	JobStatusCancelled JobStatus = "cancelled"
)

// JobType represents the kind of operation a job performs.
type JobType string

const (
	JobTypeBackup  JobType = "backup"
	JobTypeRestore JobType = "restore"
	JobTypeVerify  JobType = "verify"
	JobTypePrune   JobType = "prune"
)

// JobTrigger indicates how a job was initiated.
type JobTrigger string

const (
	JobTriggerScheduler JobTrigger = "scheduler"
	JobTriggerManual    JobTrigger = "manual"
	JobTriggerAPI       JobTrigger = "api"
)

// ─── Destination ─────────────────────────────────────────────────────────────

// DestinationType represents the storage backend for a backup destination.
type DestinationType string

const (
	DestinationTypeLocal  DestinationType = "local"
	DestinationTypeS3     DestinationType = "s3"
	DestinationTypeSFTP   DestinationType = "sftp"
	DestinationTypeRest   DestinationType = "rest"
	DestinationTypeRclone DestinationType = "rclone"
)

// ─── Auth ────────────────────────────────────────────────────────────────────

// AuthProvider identifies the authentication method used by a user.
type AuthProvider string

const (
	AuthProviderLocal AuthProvider = "local"
	AuthProviderOIDC  AuthProvider = "oidc"
)

// UserRole represents the permission level of a user.
type UserRole string

const (
	UserRoleAdmin    UserRole = "admin"
	UserRoleOperator UserRole = "operator"
	UserRoleViewer   UserRole = "viewer"
)

// ─── Notification ────────────────────────────────────────────────────────────

// NotificationChannel represents the delivery channel for a notification.
type NotificationChannel string

const (
	NotificationChannelEmail   NotificationChannel = "email"
	NotificationChannelWebhook NotificationChannel = "webhook"
	NotificationChannelInApp   NotificationChannel = "in_app"
)

// NotificationEvent represents the trigger event for a notification.
type NotificationEvent string

const (
	NotificationEventBackupSuccess NotificationEvent = "backup.success"
	NotificationEventBackupFailed  NotificationEvent = "backup.failed"
	NotificationEventBackupPartial NotificationEvent = "backup.partial"
	NotificationEventVerifyFailed  NotificationEvent = "verify.failed"
	NotificationEventAgentOffline  NotificationEvent = "agent.offline"
	NotificationEventStorageLow    NotificationEvent = "storage.low"
)

// ─── Policy ──────────────────────────────────────────────────────────────────

// RetentionPolicy defines how many snapshots to keep over time.
type RetentionPolicy struct {
	KeepLast    int `json:"keep_last,omitempty"`
	KeepHourly  int `json:"keep_hourly,omitempty"`
	KeepDaily   int `json:"keep_daily,omitempty"`
	KeepWeekly  int `json:"keep_weekly,omitempty"`
	KeepMonthly int `json:"keep_monthly,omitempty"`
	KeepYearly  int `json:"keep_yearly,omitempty"`
}

// Source defines a backup source on the agent machine.
type Source struct {
	Type  SourceType `json:"type"`
	Path  string     `json:"path,omitempty"`
	Label string     `json:"label,omitempty"`
}

// SourceType identifies the kind of data being backed up.
type SourceType string

const (
	SourceTypeDirectory    SourceType = "directory"
	SourceTypeDockerVolume SourceType = "docker_volume"
)

// Hook defines a script to run before or after a backup.
type Hook struct {
	Name        string   `json:"name"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	TimeoutSecs int      `json:"timeout_secs,omitempty"`
}

// ─── Pagination ──────────────────────────────────────────────────────────────

// Page holds pagination parameters for list queries.
type Page struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// PagedResult wraps a list result with total count for pagination.
type PagedResult[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
	Page  Page  `json:"page"`
}

// ─── Time ────────────────────────────────────────────────────────────────────

// TimeRange defines an inclusive time interval for filtering queries.
type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}