package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// SettingsHandler groups settings-related HTTP handlers.
// All routes in this handler are admin-only, enforced by RequireRole("admin")
// in the router. Two configuration namespaces are supported:
//   - OIDC: stored in the oidc_providers table via OIDCProviderRepository
//   - SMTP: stored as key-value pairs in the settings table via SettingsRepository
type SettingsHandler struct {
	oidcRepo     repositories.OIDCProviderRepository
	settingsRepo repositories.SettingsRepository
	baseURL      string // e.g. "https://arkeep.example.com" — used to build the callback URL
	logger       *zap.Logger
}

// NewSettingsHandler creates a new SettingsHandler.
// baseURL is the externally reachable URL of the server, used to compute the
// OIDC callback URL shown to administrators when configuring providers.
func NewSettingsHandler(
	oidcRepo repositories.OIDCProviderRepository,
	settingsRepo repositories.SettingsRepository,
	baseURL string,
	logger *zap.Logger,
) *SettingsHandler {
	return &SettingsHandler{
		oidcRepo:     oidcRepo,
		settingsRepo: settingsRepo,
		baseURL:      baseURL,
		logger:       logger.Named("settings_handler"),
	}
}

// callbackURL returns the OIDC redirect URI that identity providers must be
// configured to accept. It is the same for all providers.
func (h *SettingsHandler) callbackURL() string {
	return h.baseURL + "/api/v1/auth/oidc/callback"
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

func (h *SettingsHandler) oidcToResponse(p *db.OIDCProvider) oidcProviderResponse {
	return oidcProviderResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		Issuer:      p.Issuer,
		ClientID:    p.ClientID,
		CallbackURL: h.callbackURL(),
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

	resp := make([]oidcProviderResponse, len(providers))
	for i, p := range providers {
		resp[i] = h.oidcToResponse(p)
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

	Created(w, h.oidcToResponse(provider))
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

	Ok(w, h.oidcToResponse(provider))
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

	Ok(w, h.oidcToResponse(existing))
}

// DeleteOIDC handles DELETE /api/v1/settings/oidc/{id} (admin only).
func (h *SettingsHandler) DeleteOIDC(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	if err := h.oidcRepo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	NoContent(w)
}


// =============================================================================
// SMTP
// =============================================================================

type smtpResponse struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"` // always "***" on read
	From     string `json:"from"`
	TLS      bool   `json:"tls"`
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

	idx := settingsToMap(settings)
	port, _ := strconv.Atoi(idx[notification.KeySMTPPort])

	Ok(w, smtpResponse{
		Host:     idx[notification.KeySMTPHost],
		Port:     port,
		Username: idx[notification.KeySMTPUsername],
		Password: "***",
		From:     idx[notification.KeySMTPFrom],
		TLS:      idx[notification.KeySMTPTLS] == "true",
	})
}

type upsertSMTPRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	TLS      bool   `json:"tls"`
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
		{notification.KeySMTPPassword, req.Password},
		{notification.KeySMTPFrom, req.From},
		{notification.KeySMTPTLS, strconv.FormatBool(req.TLS)},
	}

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

	Ok(w, smtpResponse{
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: "***",
		From:     req.From,
		TLS:      req.TLS,
	})
}

func validateUpsertSMTP(req *upsertSMTPRequest) error {
	if req.Host == "" {
		return errors.New("host is required")
	}
	if req.Port < 1 || req.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if req.Password == "" {
		return errors.New("password is required")
	}
	if req.From == "" {
		return errors.New("from is required")
	}
	return nil
}

// =============================================================================
// Internal helpers
// =============================================================================

func settingsToMap(settings []db.Setting) map[string]string {
	m := make(map[string]string, len(settings))
	for _, s := range settings {
		m[s.Key] = string(s.Value)
	}
	return m
}
