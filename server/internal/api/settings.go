package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/logretention"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// LogPruner runs an on-demand job_logs retention sweep. Satisfied by
// *logretention.Service.
type LogPruner interface {
	RunOnce(ctx context.Context) (int64, error)
}

// SettingsHandler groups settings-related HTTP handlers.
// All routes in this handler are admin-only, enforced by RequireRole("admin")
// in the router. Two configuration namespaces are supported:
//   - OIDC: stored in the oidc_providers table via OIDCProviderRepository
//   - SMTP: stored as key-value pairs in the settings table via SettingsRepository
type SettingsHandler struct {
	oidcRepo     repositories.OIDCProviderRepository
	settingsRepo repositories.SettingsRepository
	auditRepo    repositories.AuditRepository
	logPruner    LogPruner
	logger       *zap.Logger
}

// NewSettingsHandler creates a new SettingsHandler. logPruner may be nil, in
// which case the manual log-prune endpoint is unavailable.
func NewSettingsHandler(
	oidcRepo repositories.OIDCProviderRepository,
	settingsRepo repositories.SettingsRepository,
	auditRepo repositories.AuditRepository,
	logPruner LogPruner,
	logger *zap.Logger,
) *SettingsHandler {
	return &SettingsHandler{
		oidcRepo:     oidcRepo,
		settingsRepo: settingsRepo,
		auditRepo:    auditRepo,
		logPruner:    logPruner,
		logger:       logger.Named("settings_handler"),
	}
}

// =============================================================================
// OIDC
// =============================================================================

// oidcProviderResponse is the JSON representation of an OIDC provider config.
// ClientSecret is intentionally omitted — it is write-only and never returned.
// CallbackURL is computed server-side and returned read-only for the admin to
// copy into the identity provider's application settings.
type oidcProviderResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Issuer      string `json:"issuer"`
	ClientID    string `json:"client_id"`
	CallbackURL string `json:"callback_url"` // read-only, computed from base_url
	Scopes      string `json:"scopes"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (h *SettingsHandler) oidcToResponse(p *db.OIDCProvider, callbackURL string) oidcProviderResponse {
	return oidcProviderResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		Issuer:      p.Issuer,
		ClientID:    p.ClientID,
		CallbackURL: callbackURL,
		Scopes:      p.Scopes,
		Enabled:     p.Enabled,
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// ListOIDC handles GET /api/v1/settings/oidc (admin only).
// Returns all configured OIDC providers.
func (h *SettingsHandler) ListOIDC(w http.ResponseWriter, r *http.Request) {
	providers, err := h.oidcRepo.List(r.Context())
	if err != nil {
		h.logger.Error("failed to list OIDC providers", zap.Error(err))
		ErrInternal(w)
		return
	}

	cbURL := requestCallbackURL(r)
	resp := make([]oidcProviderResponse, len(providers))
	for i, p := range providers {
		resp[i] = h.oidcToResponse(p, cbURL)
	}

	Ok(w, resp)
}

// createOIDCRequest is the JSON body for POST /api/v1/settings/oidc.
// ClientSecret is required on creation.
type createOIDCRequest struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Scopes       string `json:"scopes"`
	Enabled      bool   `json:"enabled"`
}

// CreateOIDC handles POST /api/v1/settings/oidc (admin only).
func (h *SettingsHandler) CreateOIDC(w http.ResponseWriter, r *http.Request) {
	var req createOIDCRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		ErrBadRequest(w, "name is required")
		return
	}
	if req.Issuer == "" {
		ErrBadRequest(w, "issuer is required")
		return
	}
	if req.ClientID == "" {
		ErrBadRequest(w, "client_id is required")
		return
	}
	if req.ClientSecret == "" {
		ErrBadRequest(w, "client_secret is required")
		return
	}

	if req.Scopes == "" {
		req.Scopes = "openid email profile"
	}

	provider := &db.OIDCProvider{
		Name:         req.Name,
		Issuer:       req.Issuer,
		ClientID:     req.ClientID,
		ClientSecret: db.EncryptedString(req.ClientSecret),
		Scopes:       req.Scopes,
		Enabled:      req.Enabled,
	}

	if err := h.oidcRepo.Create(r.Context(), provider); err != nil {
		h.logger.Error("failed to create OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "settings.oidc.create", "settings", provider.ID.String(), map[string]any{"name": provider.Name, "issuer": provider.Issuer})
	Created(w, h.oidcToResponse(provider, requestCallbackURL(r)))
}

// GetOIDCByID handles GET /api/v1/settings/oidc/{id} (admin only).
func (h *SettingsHandler) GetOIDCByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	provider, err := h.oidcRepo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, h.oidcToResponse(provider, requestCallbackURL(r)))
}

// updateOIDCRequest is the JSON body for PUT /api/v1/settings/oidc/{id}.
// ClientSecret is optional — omit or send empty string to keep the existing value.
type updateOIDCRequest struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"` // optional: empty = keep existing
	Scopes       string `json:"scopes"`
	Enabled      bool   `json:"enabled"`
}

// UpdateOIDC handles PUT /api/v1/settings/oidc/{id} (admin only).
// If client_secret is empty the stored encrypted secret is preserved.
func (h *SettingsHandler) UpdateOIDC(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req updateOIDCRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		ErrBadRequest(w, "name is required")
		return
	}
	if req.Issuer == "" {
		ErrBadRequest(w, "issuer is required")
		return
	}
	if req.ClientID == "" {
		ErrBadRequest(w, "client_id is required")
		return
	}

	if req.Scopes == "" {
		req.Scopes = "openid email profile"
	}

	existing, err := h.oidcRepo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get OIDC provider for update", zap.Error(err))
		ErrInternal(w)
		return
	}

	existing.Name = req.Name
	existing.Issuer = req.Issuer
	existing.ClientID = req.ClientID
	existing.Scopes = req.Scopes
	existing.Enabled = req.Enabled

	// Only overwrite the stored secret if a new one was provided.
	if req.ClientSecret != "" {
		existing.ClientSecret = db.EncryptedString(req.ClientSecret)
	}

	if err := h.oidcRepo.Update(r.Context(), existing); err != nil {
		h.logger.Error("failed to update OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "settings.oidc.update", "settings", id.String(), map[string]any{"name": existing.Name, "issuer": existing.Issuer})
	Ok(w, h.oidcToResponse(existing, requestCallbackURL(r)))
}

// DeleteOIDC handles DELETE /api/v1/settings/oidc/{id} (admin only).
func (h *SettingsHandler) DeleteOIDC(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	// Fetch name before deletion for the audit record.
	existing, _ := h.oidcRepo.GetByID(r.Context(), id)

	if err := h.oidcRepo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	name := ""
	if existing != nil {
		name = existing.Name
	}
	logAudit(r, h.auditRepo, h.logger, "settings.oidc.delete", "settings", id.String(), map[string]any{"name": name})
	NoContent(w)
}


// =============================================================================
// SMTP
// =============================================================================

type smtpResponse struct {
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Username   string   `json:"username"`
	Password   string   `json:"password"` // always "***" on read
	From       string   `json:"from"`
	FromName   string   `json:"from_name"` // optional sender display name
	TLS        bool     `json:"tls"`
	Recipients []string `json:"recipients"`
}

// GetSMTP handles GET /api/v1/settings/smtp (admin only).
func (h *SettingsHandler) GetSMTP(w http.ResponseWriter, r *http.Request) {
	settings, err := h.settingsRepo.GetMany(r.Context(), "smtp.")
	if err != nil {
		h.logger.Error("failed to load smtp settings", zap.Error(err))
		ErrInternal(w)
		return
	}

	if len(settings) == 0 {
		ErrNotFound(w)
		return
	}

	notifSettings, err := h.settingsRepo.GetMany(r.Context(), "notification.")
	if err != nil {
		h.logger.Error("failed to load notification settings", zap.Error(err))
		ErrInternal(w)
		return
	}

	idx := settingsToMap(settings)
	notifIdx := settingsToMap(notifSettings)
	port, _ := strconv.Atoi(idx[notification.KeySMTPPort])

	Ok(w, smtpResponse{
		Host:       idx[notification.KeySMTPHost],
		Port:       port,
		Username:   idx[notification.KeySMTPUsername],
		Password:   "***",
		From:       idx[notification.KeySMTPFrom],
		FromName:   idx[notification.KeySMTPFromName],
		TLS:        idx[notification.KeySMTPTLS] == "true",
		Recipients: splitRecipients(notifIdx[notification.KeyNotificationRecipients]),
	})
}

type upsertSMTPRequest struct {
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	From       string   `json:"from"`
	FromName   string   `json:"from_name"`
	TLS        bool     `json:"tls"`
	Recipients []string `json:"recipients"`
}

// UpsertSMTP handles PUT /api/v1/settings/smtp (admin only).
func (h *SettingsHandler) UpsertSMTP(w http.ResponseWriter, r *http.Request) {
	var req upsertSMTPRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := validateUpsertSMTP(&req); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}

	ctx := r.Context()

	pairs := []struct {
		key   string
		value string
	}{
		{notification.KeySMTPHost, req.Host},
		{notification.KeySMTPPort, strconv.Itoa(req.Port)},
		{notification.KeySMTPUsername, req.Username},
		{notification.KeySMTPFrom, req.From},
		{notification.KeySMTPFromName, req.FromName},
		{notification.KeySMTPTLS, strconv.FormatBool(req.TLS)},
		{notification.KeyNotificationRecipients, strings.Join(req.Recipients, ",")},
	}

	if req.Username == "" {
		// Auth disabled — clear stored password so it doesn't linger in the DB.
		pairs = append(pairs, struct{ key, value string }{notification.KeySMTPPassword, ""})
	} else if req.Password != "" {
		// Auth enabled with a new password provided.
		pairs = append(pairs, struct{ key, value string }{notification.KeySMTPPassword, req.Password})
	}
	// else: auth enabled but password field left blank → keep existing value.

	for _, p := range pairs {
		if err := h.settingsRepo.Set(ctx, p.key, db.EncryptedString(p.value)); err != nil {
			h.logger.Error("failed to save smtp setting",
				zap.String("key", p.key),
				zap.Error(err),
			)
			ErrInternal(w)
			return
		}
	}

	h.logger.Info("smtp settings updated")

	logAudit(r, h.auditRepo, h.logger, "settings.smtp.update", "settings", "", map[string]any{"host": req.Host, "port": req.Port})
	Ok(w, smtpResponse{
		Host:       req.Host,
		Port:       req.Port,
		Username:   req.Username,
		Password:   "***",
		From:       req.From,
		FromName:   req.FromName,
		TLS:        req.TLS,
		Recipients: req.Recipients,
	})
}

func validateUpsertSMTP(req *upsertSMTPRequest) error {
	if req.Host == "" {
		return errors.New("host is required")
	}
	if req.Port < 1 || req.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if req.From == "" {
		return errors.New("from is required")
	}
	return nil
}

// =============================================================================
// Notification event settings
// =============================================================================

type notificationSettingsResponse struct {
	JobSuccess   bool `json:"job_success"`
	JobFailure   bool `json:"job_failure"`
	AgentOffline bool `json:"agent_offline"`
	AgentOnline  bool `json:"agent_online"`
}

// GetNotificationSettings handles GET /api/v1/settings/notifications (admin only).
// Returns the current per-event toggle configuration with defaults applied.
func (h *SettingsHandler) GetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.settingsRepo.GetMany(r.Context(), "notification.events.")
	if err != nil {
		h.logger.Error("failed to load notification event settings", zap.Error(err))
		ErrInternal(w)
		return
	}

	idx := settingsToMap(settings)
	resp := notificationSettingsResponse{
		JobSuccess:   boolSetting(idx, notification.KeyEventJobSuccess, true),
		JobFailure:   boolSetting(idx, notification.KeyEventJobFailure, true),
		AgentOffline: boolSetting(idx, notification.KeyEventAgentOffline, true),
		AgentOnline:  boolSetting(idx, notification.KeyEventAgentOnline, false),
	}
	Ok(w, resp)
}

type upsertNotificationSettingsRequest struct {
	JobSuccess   bool `json:"job_success"`
	JobFailure   bool `json:"job_failure"`
	AgentOffline bool `json:"agent_offline"`
	AgentOnline  bool `json:"agent_online"`
}

// UpsertNotificationSettings handles PUT /api/v1/settings/notifications (admin only).
func (h *SettingsHandler) UpsertNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var req upsertNotificationSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	pairs := []struct {
		key   string
		value string
	}{
		{notification.KeyEventJobSuccess, strconv.FormatBool(req.JobSuccess)},
		{notification.KeyEventJobFailure, strconv.FormatBool(req.JobFailure)},
		{notification.KeyEventAgentOffline, strconv.FormatBool(req.AgentOffline)},
		{notification.KeyEventAgentOnline, strconv.FormatBool(req.AgentOnline)},
	}
	for _, p := range pairs {
		if err := h.settingsRepo.Set(ctx, p.key, db.EncryptedString(p.value)); err != nil {
			h.logger.Error("failed to save notification event setting",
				zap.String("key", p.key),
				zap.Error(err),
			)
			ErrInternal(w)
			return
		}
	}

	logAudit(r, h.auditRepo, h.logger, "settings.notifications.update", "settings", "", nil)
	Ok(w, notificationSettingsResponse(req))
}

// =============================================================================
// Job-log retention settings
// =============================================================================

// maxRetentionDays caps the retention window at a sane upper bound (~10 years).
const maxRetentionDays = 3650

type logRetentionSettings struct {
	InfoDays      int `json:"info_days"`
	WarnErrorDays int `json:"warn_error_days"`
}

// GetLogRetention handles GET /api/v1/settings/logs (admin only).
// Returns the current retention windows in days. 0 means "keep forever".
func (h *SettingsHandler) GetLogRetention(w http.ResponseWriter, r *http.Request) {
	settings, err := h.settingsRepo.GetMany(r.Context(), "logs.retention.")
	if err != nil {
		h.logger.Error("failed to load log retention settings", zap.Error(err))
		ErrInternal(w)
		return
	}

	idx := settingsToMap(settings)
	Ok(w, logRetentionSettings{
		InfoDays:      intSetting(idx, logretention.KeyInfoRetentionDays),
		WarnErrorDays: intSetting(idx, logretention.KeyWarnErrorRetentionDays),
	})
}

// UpsertLogRetention handles PUT /api/v1/settings/logs (admin only).
func (h *SettingsHandler) UpsertLogRetention(w http.ResponseWriter, r *http.Request) {
	var req logRetentionSettings
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.InfoDays < 0 || req.WarnErrorDays < 0 {
		ErrBadRequest(w, "retention days must be zero or greater")
		return
	}
	if req.InfoDays > maxRetentionDays || req.WarnErrorDays > maxRetentionDays {
		ErrBadRequest(w, "retention days is too large")
		return
	}

	ctx := r.Context()
	pairs := []struct {
		key   string
		value string
	}{
		{logretention.KeyInfoRetentionDays, strconv.Itoa(req.InfoDays)},
		{logretention.KeyWarnErrorRetentionDays, strconv.Itoa(req.WarnErrorDays)},
	}
	for _, p := range pairs {
		if err := h.settingsRepo.Set(ctx, p.key, db.EncryptedString(p.value)); err != nil {
			h.logger.Error("failed to save log retention setting",
				zap.String("key", p.key),
				zap.Error(err),
			)
			ErrInternal(w)
			return
		}
	}

	logAudit(r, h.auditRepo, h.logger, "settings.logs.update", "settings", "", map[string]any{
		"info_days":       req.InfoDays,
		"warn_error_days": req.WarnErrorDays,
	})
	Ok(w, req)
}

// PruneLogsNow handles POST /api/v1/settings/logs/prune (admin only). It runs a
// retention sweep immediately using the currently saved settings, so an admin
// with an already-bloated database does not have to wait for the daily run.
func (h *SettingsHandler) PruneLogsNow(w http.ResponseWriter, r *http.Request) {
	if h.logPruner == nil {
		ErrServiceUnavailable(w, "log retention is not available")
		return
	}
	deleted, err := h.logPruner.RunOnce(r.Context())
	if err != nil {
		h.logger.Error("manual log prune failed", zap.Error(err))
		ErrInternal(w)
		return
	}
	logAudit(r, h.auditRepo, h.logger, "settings.logs.prune", "settings", "", map[string]any{
		"deleted": deleted,
	})
	Ok(w, map[string]any{"deleted": deleted})
}

// =============================================================================
// Internal helpers
// =============================================================================

func splitRecipients(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var out []string
	for _, r := range strings.Split(raw, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// intSetting parses a stored setting as a non-negative int, returning 0 when the
// key is absent or unparseable.
func intSetting(idx map[string]string, key string) int {
	v, ok := idx[key]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func boolSetting(idx map[string]string, key string, defaultVal bool) bool {
	v, ok := idx[key]
	if !ok {
		return defaultVal
	}
	return v == "true"
}

func settingsToMap(settings []db.Setting) map[string]string {
	m := make(map[string]string, len(settings))
	for _, s := range settings {
		m[s.Key] = string(s.Value)
	}
	return m
}
