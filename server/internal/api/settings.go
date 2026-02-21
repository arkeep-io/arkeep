package api

import (
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repository"
)

// SettingsHandler groups settings-related HTTP handlers.
// Currently only OIDC provider configuration is exposed. All routes in this
// handler are admin-only, enforced by RequireRole("admin") in the router.
type SettingsHandler struct {
	oidcRepo repository.OIDCProviderRepository
	logger   *zap.Logger
}

// NewSettingsHandler creates a new SettingsHandler.
func NewSettingsHandler(oidcRepo repository.OIDCProviderRepository, logger *zap.Logger) *SettingsHandler {
	return &SettingsHandler{
		oidcRepo: oidcRepo,
		logger:   logger.Named("settings_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

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

// oidcProviderToResponse converts a db.OIDCProvider to an oidcProviderResponse.
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

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// GetOIDC handles GET /api/v1/settings/oidc (admin only).
// Returns the currently configured OIDC provider, or 404 if none is configured.
func (h *SettingsHandler) GetOIDC(w http.ResponseWriter, r *http.Request) {
	provider, err := h.oidcRepo.GetEnabled(r.Context())
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
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
// PUT semantics: the entire configuration is replaced on each call.
// Only one OIDC provider is supported at a time in the open core tier.
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
// Creates the OIDC provider configuration if none exists, or replaces the
// existing one. Only one provider is supported — this is not a multi-provider
// endpoint. ClientSecret is encrypted at rest automatically by EncryptedString.
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

	// Check if a provider already exists — update in place if so, create otherwise.
	existing, err := h.oidcRepo.GetEnabled(r.Context())
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		h.logger.Error("failed to check existing OIDC provider", zap.Error(err))
		ErrInternal(w)
		return
	}

	if existing != nil {
		// Update existing provider.
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

	// No provider exists — create a new one.
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

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

// validateUpsertOIDC checks required fields for OIDC provider configuration.
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