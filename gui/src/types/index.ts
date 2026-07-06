// types/index.ts — Shared TypeScript interfaces for the Arkeep GUI.
//
// These types mirror the JSON shapes returned by the REST API (server/internal/api/).
// Keep them in sync with the Go structs in server/internal/db/models.go and the
// API response helpers in server/internal/api/response.go.
//
// Naming convention:
//   - Types that map directly to database models use the model name (Agent, Policy, …)
//   - Request/response envelopes use the suffix Request / Response
//   - Enum-like string unions are defined as const objects + typeof for autocomplete

// ─── Enums ────────────────────────────────────────────────────────────────────

export const UserRole = {
  Admin: 'admin',
  User: 'user',
} as const
export type UserRole = (typeof UserRole)[keyof typeof UserRole]

export const AgentStatus = {
  Online: 'online',
  Offline: 'offline',
  Unknown: 'unknown',
} as const
export type AgentStatus = (typeof AgentStatus)[keyof typeof AgentStatus]

export const JobStatus = {
  Pending: 'pending',
  Running: 'running',
  Succeeded: 'succeeded',
  Failed: 'failed',
  Cancelled: 'cancelled',
} as const
export type JobStatus = (typeof JobStatus)[keyof typeof JobStatus]
export const JobType = {
  Backup: 'backup',
  Restore: 'restore',
} as const
export type JobType = (typeof JobType)[keyof typeof JobType]

export const DestinationType = {
  Local: 'local',
  S3: 's3',
  SFTP: 'sftp',
  RestServer: 'rest',
  Rclone: 'rclone',
} as const
export type DestinationType = (typeof DestinationType)[keyof typeof DestinationType]

export const SourceType = {
  Path: 'path',
  DockerVolume: 'docker-volume',
} as const
export type SourceType = (typeof SourceType)[keyof typeof SourceType]

export const NotificationChannel = {
  InApp: 'in_app',
  Email: 'email',
  Webhook: 'webhook',
} as const
export type NotificationChannel = (typeof NotificationChannel)[keyof typeof NotificationChannel]

export const NotificationEventType = {
  JobSucceeded: 'job.succeeded',
  JobFailed: 'job.failed',
  AgentOffline: 'agent.offline',
} as const
export type NotificationEventType =
  (typeof NotificationEventType)[keyof typeof NotificationEventType]

// ─── Core models ──────────────────────────────────────────────────────────────

export interface User {
  id: string
  email: string
  display_name: string
  role: UserRole
  is_active: boolean
  is_oidc: boolean      // true for OIDC-provisioned accounts
  last_login_at: string | null
  created_at: string
}

export interface Agent {
  id: string
  name: string
  hostname: string
  os: string
  arch: string
  status: AgentStatus
  version: string
  docker_available: boolean
  last_seen_at: string | null
  created_at: string
  updated_at: string
  // deleted_at is omitted — soft-deleted agents are not returned by the API
}

// AgentMetrics are sent by the agent on each heartbeat and stored in memory
// by the server (not persisted to the database).
export interface AgentMetrics {
  cpu_percent: number
  ram_used_bytes: number
  ram_total_bytes: number
  disk_used_bytes: number
  disk_total_bytes: number
}

// VolumeInfo is returned by GET /api/v1/agents/{id}/volumes.
// Mirrors the Docker volume metadata exposed by the agent's docker package.
export interface VolumeInfo {
  name: string
  mountpoint: string
  driver: string
}

// ─── Destination ──────────────────────────────────────────────────────────────

export interface Destination {
  id: string
  name: string
  type: DestinationType
  config: string
  // repository_password is always masked ("***") on read
  repository_password: string
  enabled: boolean
  created_at: string
  updated_at: string
  // repo_size_bytes is the real deduplicated on-disk size of this destination's
  // restic repository, refreshed after each backup/import. 0 until first measured.
  repo_size_bytes: number
  repo_size_updated_at: string // RFC3339, empty until first measured
}

// ─── Policy ───────────────────────────────────────────────────────────────────

export interface PolicySource {
  type: SourceType
  path: string // filesystem path or docker volume name
}

export interface RetentionConfig {
  keep_last: number
  keep_hourly: number
  keep_daily: number
  keep_weekly: number
  keep_monthly: number
  keep_yearly: number
}

export interface HookConfig {
  pre_backup: string[] // shell commands to run before backup
  post_backup: string[] // shell commands to run after backup (regardless of outcome)
  timeout_seconds: number
}

export interface PolicyDestination {
  destination_id: string
  destination_name: string // denormalized for display; populated by server join
  priority: number // lower = higher priority; used for 3-2-1 ordering
}

export interface Policy {
  id: string
  name: string
  agent_id: string
  agent_name: string
  sources: string           // JSON string — parse client-side when needed
  schedule: string
  retention_last: number
  retention_hourly: number
  retention_daily: number
  retention_weekly: number
  retention_monthly: number
  retention_yearly: number
  hook_pre_backup: string   // JSON string or empty
  hook_post_backup: string  // JSON string or empty
  exclude_patterns: string  // JSON array string or empty
  enabled: boolean
  destinations: PolicyDestination[]
  last_run_at: string | null
  next_run_at: string | null
  created_at: string
  updated_at: string
}

// PolicyListItem is the leaner shape returned by the list endpoint.
// Destinations are NOT included (too costly — N extra queries per policy).
export type PolicyListItem = Omit<Policy, 'destinations'>

// ─── Job ──────────────────────────────────────────────────────────────────────

export interface JobDestination {
  id: string
  destination_id: string
  destination_name: string  // denormalized via JOIN in the API layer
  status: JobStatus
  snapshot_id: string       // opaque ID returned by the backup engine
  size_bytes: number        // total repository size after backup (SizeBytes in DB)
  started_at: string | null
  ended_at: string | null
  error: string
}

export interface JobLog {
  id: string
  level: 'debug' | 'info' | 'warn' | 'error'
  message: string
  timestamp: string   // maps to Timestamp field in db.JobLog
}

export interface Job {
  id: string
  policy_id: string
  policy_name: string   // denormalized via JOIN in the API layer
  agent_id: string
  agent_name: string    // denormalized via JOIN in the API layer
  type: JobType
  status: JobStatus
  error: string
  started_at: string | null
  ended_at: string | null
  created_at: string
  // Populated only on GetByID (detail endpoint)
  destinations?: JobDestination[]
}

// JobListItem is the leaner shape returned by the list endpoint.
export type JobListItem = Omit<Job, 'destinations'>

// ─── Snapshot Browse ──────────────────────────────────────────────────────────

export interface SnapshotFileEntry {
  path: string
  type: 'file' | 'dir'
  size: number
  mtime: string
}

export interface SnapshotBrowseResponse {
  entries: SnapshotFileEntry[]
}

// ─── Snapshot ─────────────────────────────────────────────────────────────────

export interface Snapshot {
  id: string
  policy_id: string | null
  policy_name: string
  destination_id: string
  destination_name: string
  agent_id: string
  agent_name: string
  job_id: string | null
  restic_snapshot_id: string
  size_bytes: number
  tags: string
  hostname: string
  is_imported: boolean
  created_at: string
}

// ─── Notification ─────────────────────────────────────────────────────────────

// Notification mirrors the notificationResponse JSON shape returned by
// GET /api/v1/notifications.
export interface Notification {
  id: string
  type: string       // "job_success" | "job_failure" | "agent_offline"
  title: string
  body: string
  payload: string    // JSON string with extra event context
  read_at: string | null
  created_at: string
}

// ─── Settings ─────────────────────────────────────────────────────────────────

// SMTPSettings maps to the smtp.* keys in the settings table.
export interface SMTPSettings {
  host: string
  port: number
  username: string
  password: string // write-only — always returned masked from the API
  from: string
  from_name: string // optional sender display name; defaults to "Arkeep" when empty
  tls: boolean
  recipients: string[] // explicit email recipient list; empty = all active admins
}

// WebhookSettings maps to the webhook.* keys in the settings table.
export interface WebhookSettings {
  url: string
  secret: string // HMAC signing secret — write-only, returned masked
  enabled: boolean
}

// NotificationSettings controls which event types trigger external delivery
// (email + webhook). In-app notifications are always shown regardless.
export interface NotificationSettings {
  job_success: boolean
  job_failure: boolean
  agent_offline: boolean
  agent_online: boolean
}

// LogRetentionSettings controls automatic pruning of job_logs rows. Days are
// counted from each log line's timestamp; 0 means "keep forever" (disabled).
export interface LogRetentionSettings {
  info_days: number
  warn_error_days: number
}

// OIDCProvider maps to the oidc_providers table (admin settings view).
// callback_url is computed server-side and returned read-only — copy it into
// the identity provider's allowed redirect URIs.
export interface OIDCProvider {
  id: string
  name: string
  issuer: string
  client_id: string
  callback_url: string // read-only, computed by the server
  scopes: string
  enabled: boolean
  created_at: string
  updated_at: string
}

// OIDCProviderSummary is the minimal shape returned by the public
// GET /api/v1/auth/oidc/providers endpoint used by the login page.
export interface OIDCProviderSummary {
  id: string
  name: string
}

// ImportDestinationRequest is sent to POST /api/v1/destinations/{id}/import.
export interface ImportDestinationRequest {
  agent_id: string
  repo_password: string
}

// ImportDestinationResponse is returned by the import endpoint.
export interface ImportDestinationResponse {
  found: number
  imported: number
}

// CreateDestinationResponse is returned by POST /api/v1/destinations.
// The import field is present only when import_agent_id was supplied in the request.
export interface CreateDestinationResponse extends Destination {
  import?: ImportDestinationResponse
}

// ─── API request / response shapes ───────────────────────────────────────────

// Pagination params accepted by list endpoints
export interface PaginationParams {
  page?: number
  per_page?: number
}

// Standard paginated list envelope
export interface PaginatedResponse<T> {
  items: T[]
  total: number
}

// Auth — login returns only the access token; user profile is a separate call
export interface LoginRequest {
  email: string
  password: string
}

export interface TokenResponse {
  access_token: string
  expires_in: number
}

// Agents
export interface CreateAgentRequest {
  name: string
}

export interface UpdateAgentRequest {
  name: string
}

export interface AgentRegistrationToken {
  token: string
  expires_at: string
}

// Destinations
export interface CreateDestinationRequest {
  name: string
  type: DestinationType
  config: string
  repository_password: string
}

export type UpdateDestinationRequest = Partial<CreateDestinationRequest>

// Policies
export interface CreatePolicyRequest {
  name: string
  agent_id: string
  sources: PolicySource[]
  schedule: string
  retention: RetentionConfig
  hooks?: HookConfig
  enabled: boolean
  destination_ids: { destination_id: string; priority: number }[]
}

export type UpdatePolicyRequest = Partial<CreatePolicyRequest>

// Users
export interface CreateUserRequest {
  email: string
  password: string
  display_name: string
  role: UserRole
}

export interface UpdateUserRequest {
  display_name?: string
  role?: UserRole
  is_active?: boolean
  password?: string
}

// Self-update — users can only change their own display_name and password.
// OIDC users cannot change password (managed by the IdP).
export interface UpdateMeRequest {
  display_name?: string
  password?: string
}

// Snapshots
export interface RestoreRequest {
  agent_id: string
  target_path: string
  include_paths?: string[]
}

export interface RestoreResponse {
  job_id: string
}

export interface TriggerResponse {
  job_id: string
}

export interface VersionInfo {
  server_version: string
  latest_version: string
  update_available: boolean
}

// ResticProgressEvent is emitted by restic --json during backup/restore and
// arrives on the WebSocket as a log message whose message field is JSON.
export interface ResticProgressEvent {
  message_type: 'status' | 'summary' | 'error'
  percent_done: number
  files_new: number
  files_done: number
  bytes_done: number
  total_files: number
  total_bytes: number
  snapshot_id?: string
  total_bytes_processed?: number
  data_added?: number
}

// ─── WebSocket message payloads ───────────────────────────────────────────────
// These types describe the `payload` field of WSMessage for each topic type.
// They are used in conjunction with services/websocket.ts.

export interface JobStatusPayload {
  job_id: string
  status: JobStatus
  error?: string
  started_at?: string
  finished_at?: string
}

export interface JobLogPayload {
  job_id: string
  level: JobLog['level']
  message: string
  timestamp: string
}

export interface AgentStatusPayload {
  agent_id: string
  status: AgentStatus
  metrics?: AgentMetrics
  last_seen_at: string
}

// WSNotificationPayload is the shape of msg.payload when msg.type === 'notification'.
// The server sends the notification fields directly (not wrapped in a 'notification' key).
// The payload field here is an object (not a JSON string like in the REST response).
export interface WSNotificationPayload {
  id: string
  type: string
  title: string
  body: string
  payload: Record<string, unknown>
  created_at: string
}

// AuditLog represents a single admin-action audit record returned by
// GET /api/v1/admin/audit. The details field is a JSON object whose
// shape varies by action type (e.g. policy events include "name",
// auth events include "email").
export interface AuditLog {
  id: string
  user_id: string
  user_email: string
  action: string
  resource_type: string
  resource_id: string
  details: Record<string, unknown>
  ip_address: string
  created_at: string
}

// Standard response envelope returned by all server endpoints via Ok()
export interface ApiResponse<T> {
  data: T
}