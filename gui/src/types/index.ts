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
  retention_daily: number
  retention_weekly: number
  retention_monthly: number
  retention_yearly: number
  hook_pre_backup: string   // JSON string or empty
  hook_post_backup: string  // JSON string or empty
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

// ─── Snapshot ─────────────────────────────────────────────────────────────────

export interface Snapshot {
  id: string
  policy_id: string
  policy_name: string // denormalized for display
  destination_id: string
  destination_name: string // denormalized for display
  restic_snapshot_id: string // the actual Restic snapshot hash
  hostname: string
  paths: string[]
  tags: string[]
  size_bytes: number
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
  tls: boolean
  recipients: string[] // explicit email recipient list; empty = all active admins
}

// WebhookSettings maps to the webhook.* keys in the settings table.
export interface WebhookSettings {
  url: string
  secret: string // HMAC signing secret — write-only, returned masked
  enabled: boolean
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

// Standard response envelope returned by all server endpoints via Ok()
export interface ApiResponse<T> {
  data: T
}