package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// NotificationProviderHandler manages the notification providers (Gotify,
// ntfy, Telegram...) events are sent to. All routes are admin only.
type NotificationProviderHandler struct {
	repo      repositories.NotificationProviderRepository
	auditRepo repositories.AuditRepository
	logger    *zap.Logger
}

// NewNotificationProviderHandler creates a NotificationProviderHandler.
func NewNotificationProviderHandler(
	repo repositories.NotificationProviderRepository,
	auditRepo repositories.AuditRepository,
	logger *zap.Logger,
) *NotificationProviderHandler {
	return &NotificationProviderHandler{
		repo:      repo,
		auditRepo: auditRepo,
		logger:    logger.Named("notification_provider_handler"),
	}
}

// providerPriorities is shoutrrr's priority parameter per event type; empty
// leaves the service default.
type providerPriorities struct {
	JobSuccess   string `json:"job_success"`
	JobFailure   string `json:"job_failure"`
	AgentOffline string `json:"agent_offline"`
	AgentOnline  string `json:"agent_online"`
}

// notificationProviderResponse is a provider as the API returns it. Secret
// fields are write-only: only the keys of those that are set are returned.
type notificationProviderResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Enabled    bool               `json:"enabled"`
	Config     map[string]string  `json:"config"`
	SecretsSet []string           `json:"secrets_set"`
	Priorities providerPriorities `json:"priorities"`
	CreatedAt  string             `json:"created_at"`
	UpdatedAt  string             `json:"updated_at"`
}

// notificationProviderRequest is the body of create, update and test. On
// update and test, a blank secret field keeps the stored value.
type notificationProviderRequest struct {
	// ID is read by test only: the stored provider whose secrets fill the
	// blank secret fields.
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Enabled    bool               `json:"enabled"`
	Fields     map[string]string  `json:"fields"`
	Priorities providerPriorities `json:"priorities"`
	// ClearSecrets lists the stored secret fields to remove on update, since
	// a blank one is kept. A required field cannot be cleared.
	ClearSecrets []string `json:"clear_secrets"`
}

const (
	maxProviderNameLen     = 100
	maxProviderFieldLen    = 2048
	maxProviderPriorityLen = 32
)

// List handles GET /api/v1/settings/notification-providers.
func (h *NotificationProviderHandler) List(w http.ResponseWriter, r *http.Request) {
	providers, err := h.repo.List(r.Context())
	if err != nil {
		h.logger.Error("failed to list notification providers", zap.Error(err))
		ErrInternal(w)
		return
	}
	out := make([]notificationProviderResponse, 0, len(providers))
	for _, p := range providers {
		resp, err := providerToResponse(p)
		if err != nil {
			h.logger.Error("failed to decode notification provider", zap.String("id", p.ID.String()), zap.Error(err))
			ErrInternal(w)
			return
		}
		out = append(out, resp)
	}
	Ok(w, out)
}

// Create handles POST /api/v1/settings/notification-providers.
func (h *NotificationProviderHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req notificationProviderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p := &db.NotificationProvider{}
	if err := applyProviderRequest(p, &req, nil); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}
	if err := h.repo.Create(r.Context(), p); err != nil {
		h.logger.Error("failed to create notification provider", zap.Error(err))
		ErrInternal(w)
		return
	}
	h.respond(w, r, p, "settings.notification_provider.create", http.StatusCreated)
}

// Update handles PUT /api/v1/settings/notification-providers/{id}. The type
// cannot be changed.
func (h *NotificationProviderHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req notificationProviderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, ok := h.load(w, r, id)
	if !ok {
		return
	}
	stored, err := notification.ProviderSettingsFrom(p)
	if err != nil {
		h.logger.Error("failed to decode notification provider", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}
	if req.Type != p.Type {
		ErrBadRequest(w, "type cannot be changed")
		return
	}
	if err := applyProviderRequest(p, &req, stored.Secrets); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}
	if err := h.repo.Update(r.Context(), p); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to update notification provider", zap.Error(err))
		ErrInternal(w)
		return
	}
	h.respond(w, r, p, "settings.notification_provider.update", http.StatusOK)
}

// Delete handles DELETE /api/v1/settings/notification-providers/{id}. Its
// pending deliveries are deleted with it.
func (h *NotificationProviderHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	existing, _ := h.repo.GetByID(r.Context(), id)
	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete notification provider", zap.Error(err))
		ErrInternal(w)
		return
	}
	details := map[string]any{}
	if existing != nil {
		details["name"], details["type"] = existing.Name, existing.Type
	}
	logAudit(r, h.auditRepo, h.logger, "settings.notification_provider.delete", "settings", id.String(), details)
	NoContent(w)
}

// Test handles POST /api/v1/settings/notification-providers/test. It sends a
// test message with the provider of the form, saved or not; with an id, the
// stored secrets fill the blank secret fields, so they never go back to the
// browser. The provider does not need to be enabled.
func (h *NotificationProviderHandler) Test(w http.ResponseWriter, r *http.Request) {
	var req notificationProviderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var stored map[string]string
	if req.ID != "" {
		id, err := uuid.Parse(req.ID)
		if err != nil {
			ErrBadRequest(w, "id must be a valid UUID")
			return
		}
		p, ok := h.load(w, r, id)
		if !ok {
			return
		}
		if p.Type != req.Type {
			ErrBadRequest(w, "type cannot be changed")
			return
		}
		s, err := notification.ProviderSettingsFrom(p)
		if err != nil {
			h.logger.Error("failed to decode notification provider", zap.String("id", id.String()), zap.Error(err))
			ErrInternal(w)
			return
		}
		stored = s.Secrets
	}
	req.Name = "test" // a test needs no name
	p := &db.NotificationProvider{}
	if err := applyProviderRequest(p, &req, stored); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}
	settings, err := notification.ProviderSettingsFrom(p)
	if err != nil {
		ErrInternal(w)
		return
	}
	err = notification.SendTestProvider(r.Context(), settings)
	logAudit(r, h.auditRepo, h.logger, "settings.notification_provider.test", "settings", req.ID,
		map[string]any{"type": req.Type, "ok": err == nil})
	if err != nil {
		ErrUnprocessable(w, err.Error())
		return
	}
	Ok(w, map[string]bool{"ok": true})
}

// load returns the provider id, answering 404 or 500 itself when it cannot.
func (h *NotificationProviderHandler) load(w http.ResponseWriter, r *http.Request, id uuid.UUID) (*db.NotificationProvider, bool) {
	p, err := h.repo.GetByID(r.Context(), id)
	if errors.Is(err, repositories.ErrNotFound) {
		ErrNotFound(w)
		return nil, false
	}
	if err != nil {
		h.logger.Error("failed to load notification provider", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return nil, false
	}
	return p, true
}

// respond audits a create or update of p and answers with it.
func (h *NotificationProviderHandler) respond(w http.ResponseWriter, r *http.Request, p *db.NotificationProvider, action string, status int) {
	resp, err := providerToResponse(p)
	if err != nil {
		ErrInternal(w)
		return
	}
	logAudit(r, h.auditRepo, h.logger, action, "settings", p.ID.String(),
		map[string]any{"name": p.Name, "type": p.Type, "enabled": p.Enabled})
	if status == http.StatusCreated {
		Created(w, resp)
		return
	}
	Ok(w, resp)
}

// applyProviderRequest validates req and writes it into p. storedSecrets, the
// secrets of the provider being updated or tested, fill the secret fields left
// blank. The provider is validated as a whole (notification.ValidateProvider),
// so a saved provider always yields a URL its service accepts.
func applyProviderRequest(p *db.NotificationProvider, req *notificationProviderRequest, storedSecrets map[string]string) error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errors.New("name is required")
	}
	if len(name) > maxProviderNameLen || hasControlChars(name) {
		return fmt.Errorf("name must be at most %d characters, without control characters", maxProviderNameLen)
	}
	if !notification.IsProviderType(req.Type) {
		return errors.New("type is not a supported notification provider")
	}
	for k, v := range req.Fields {
		if len(v) > maxProviderFieldLen || hasControlChars(v) {
			return fmt.Errorf("%s must be at most %d characters, without control characters", k, maxProviderFieldLen)
		}
	}
	priorities := map[string]string{
		"job_success":   strings.TrimSpace(req.Priorities.JobSuccess),
		"job_failure":   strings.TrimSpace(req.Priorities.JobFailure),
		"agent_offline": strings.TrimSpace(req.Priorities.AgentOffline),
		"agent_online":  strings.TrimSpace(req.Priorities.AgentOnline),
	}
	for k, v := range priorities {
		if len(v) > maxProviderPriorityLen || hasControlChars(v) {
			return fmt.Errorf("%s priority must be at most %d characters", k, maxProviderPriorityLen)
		}
	}

	config, secrets, err := notification.SplitProviderFields(req.Type, req.Fields)
	if err != nil {
		return err
	}
	for _, k := range notification.ProviderSecretKeys(req.Type) {
		if slices.Contains(req.ClearSecrets, k) {
			continue
		}
		if _, set := secrets[k]; !set && storedSecrets[k] != "" {
			secrets[k] = storedSecrets[k]
		}
	}
	if err := notification.ValidateProvider(notification.ProviderSettings{
		Type: req.Type, Config: config, Secrets: secrets, Priorities: priorities,
	}); err != nil {
		return err
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	secretsJSON, err := json.Marshal(secrets)
	if err != nil {
		return err
	}
	p.Name = name
	p.Type = req.Type
	p.Enabled = req.Enabled
	p.Config = string(configJSON)
	p.Secrets = db.EncryptedString(secretsJSON)
	p.PriorityJobSuccess = priorities["job_success"]
	p.PriorityJobFailure = priorities["job_failure"]
	p.PriorityAgentOffline = priorities["agent_offline"]
	p.PriorityAgentOnline = priorities["agent_online"]
	return nil
}

// providerToResponse converts p for the API, leaving its secrets out.
func providerToResponse(p *db.NotificationProvider) (notificationProviderResponse, error) {
	s, err := notification.ProviderSettingsFrom(p)
	if err != nil {
		return notificationProviderResponse{}, err
	}
	secretsSet := make([]string, 0, len(s.Secrets))
	for k, v := range s.Secrets {
		if v != "" {
			secretsSet = append(secretsSet, k)
		}
	}
	slices.Sort(secretsSet)
	return notificationProviderResponse{
		ID:         p.ID.String(),
		Name:       p.Name,
		Type:       p.Type,
		Enabled:    p.Enabled,
		Config:     s.Config,
		SecretsSet: secretsSet,
		Priorities: providerPriorities{
			JobSuccess:   p.PriorityJobSuccess,
			JobFailure:   p.PriorityJobFailure,
			AgentOffline: p.PriorityAgentOffline,
			AgentOnline:  p.PriorityAgentOnline,
		},
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}
