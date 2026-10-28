ALTER TABLE two_factor_challenges DROP COLUMN session_expires_at;
ALTER TABLE refresh_tokens DROP COLUMN session_expires_at;
ALTER TABLE oidc_providers DROP COLUMN session_max_hours;
