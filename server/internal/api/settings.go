package api

import (
	"errors"
	"net/http"
	"strconv"

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
	logger       *zap.Logger
}

// NewSettingsHandler creates a new SettingsHandler.
func NewSettingsHandler(
	oidcRepo repositories.OIDCProviderRepository,
	settingsRepo repositories.SettingsRepository,
	logger *zap.Logger,
) *SettingsHandler {
	return &SettingsHandler{
		oidcRepo:     oidcRepo,
		settingsRepo: settingsRepo,
		logger:       logger.Named("settings_handler"),
	}
}

// =============================================================================
// OIDC
// =============================================================================

// oidcProviderResponse is the JSON representation of an OIDC provider config.
// ClientSecret is intentionally omitted — it is write-only and never returned.
type oidcProviderResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Issuer      string `json:"issuer"`
	ClientID    string `json:"client_id"`
	RedirectURL string `json:"redirect_url"`
	Scopes      string `json:"scopes"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func oidcProviderToResponse(p *db.OIDCProvider) oidcProviderResponse {
	return oidcProviderResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		Issuer:      p.Issuer,
		ClientID:    p.ClientID,
		RedirectURL: p.RedirectURL,
		Scopes:      p.Scopes,
		Enabled:     p.Enabled,
		CreatedAt:   p.CreatedAt.UTC().String(),
		UpdatedAt:   p.UpdatedAt.UTC().String(),
	}
}

// GetOIDC handles GET /api/v1/settings/oidc (admin only).
// Returns the currently configured OIDC provider, or 404 if none is configured.
func (h *SettingsHandler) GetOIDC(w http.ResponseWriter, r *http.Request) {
	provider, err := h.oidcRepo.GetEnabled(r.Context())
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, oidcProviderToResponse(provider))
}

// upsertOIDCRequest is the JSON body expected by PUT /api/v1/settings/oidc.
type upsertOIDCRequest struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURL  string `json:"redirect_url"`
	Scopes       string `json:"scopes"`
	Enabled      bool   `json:"enabled"`
}

// UpsertOIDC handles PUT /api/v1/settings/oidc (admin only).
// Creates the OIDC provider configuration if none exists, or replaces it.
// Only one provider is supported in the open core tier.
func (h *SettingsHandler) UpsertOIDC(w http.ResponseWriter, r *http.Request) {
	var req upsertOIDCRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := validateUpsertOIDC(&req); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}

	if req.Scopes == "" {
		req.Scopes = "openid email profile"
	}

	existing, err := h.oidcRepo.GetEnabled(r.Context())
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		h.logger.Error("failed to check existing OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	if existing != nil {
		existing.Name = req.Name
		existing.Issuer = req.Issuer
		existing.ClientID = req.ClientID
		existing.ClientSecret = db.EncryptedString(req.ClientSecret)
		existing.RedirectURL = req.RedirectURL
		existing.Scopes = req.Scopes
		existing.Enabled = req.Enabled

		if err := h.oidcRepo.Update(r.Context(), existing); err != nil {
			h.logger.Error("failed to update OIDC provider", zap.Error(err))
			ErrInternal(w)
			return
		}

		Ok(w, oidcProviderToResponse(existing))
		return
	}

	provider := &db.OIDCProvider{
		Name:         req.Name,
		Issuer:       req.Issuer,
		ClientID:     req.ClientID,
		ClientSecret: db.EncryptedString(req.ClientSecret),
		RedirectURL:  req.RedirectURL,
		Scopes:       req.Scopes,
		Enabled:      req.Enabled,
	}

	if err := h.oidcRepo.Create(r.Context(), provider); err != nil {
		h.logger.Error("failed to create OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	Created(w, oidcProviderToResponse(provider))
}

func validateUpsertOIDC(req *upsertOIDCRequest) error {
	if req.Name == "" {
		return errors.New("name is required")
	}
	if req.Issuer == "" {
		return errors.New("issuer is required")
	}
	if req.ClientID == "" {
		return errors.New("client_id is required")
	}
	if req.ClientSecret == "" {
		return errors.New("client_secret is required")
	}
	if req.RedirectURL == "" {
		return errors.New("redirect_url is required")
	}
	return nil
}

// =============================================================================
// SMTP
// =============================================================================

// smtpResponse is the JSON representation of the SMTP configuration.
// Password is always masked — it is write-only, identical to how OIDC handles
// client_secret. Callers must re-submit the password on every PUT.
type smtpResponse struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"` // always "***" on read
	From     string `json:"from"`
	TLS      bool   `json:"tls"`
}

// GetSMTP handles GET /api/v1/settings/smtp (admin only).
// Returns the current SMTP configuration with the password masked.
// Returns 404 if SMTP has not been configured yet.
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
		Password: "***", // never expose the stored credential
		From:     idx[notification.KeySMTPFrom],
		TLS:      idx[notification.KeySMTPTLS] == "true",
	})
}

// upsertSMTPRequest is the JSON body expected by PUT /api/v1/settings/smtp.
// All fields are required on every PUT — there is no partial-update semantic.
// Password must always be provided; the previous value is overwritten.
type upsertSMTPRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	TLS      bool   `json:"tls"`
}

// UpsertSMTP handles PUT /api/v1/settings/smtp (admin only).
// Writes each SMTP field as an individual key in the settings table using the
// notification package's canonical key constants. The password is encrypted at
// rest automatically via EncryptedString.
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

	// Persist each field independently. The settings table uses a key-value
	// model — there is no single "smtp" row, just smtp.host, smtp.port, etc.
	// EncryptedString encrypts the password at rest transparently on Set.
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

// settingsToMap converts a slice of db.Setting to a map[key]value string for
// O(1) lookup. EncryptedString decrypts the value automatically on cast.
func settingsToMap(settings []db.Setting) map[string]string {
	m := make(map[string]string, len(settings))
	for _, s := range settings {
		m[s.Key] = string(s.Value)
	}
	return m
}