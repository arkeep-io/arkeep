-- Caps OIDC sessions (issue #277): a refresh token keeps rotating for 7 days
-- after each use, so without an absolute limit a user removed from the
-- provider's allowed groups would never pass through the IdP again.
-- session_max_hours is set per provider; session_expires_at is the absolute
-- deadline of the session, set at the OIDC login, copied on every refresh and
-- carried through the two-factor challenge. NULL means no deadline (local
-- logins).
ALTER TABLE oidc_providers ADD COLUMN session_max_hours INTEGER NOT NULL DEFAULT 12;
ALTER TABLE refresh_tokens ADD COLUMN session_expires_at TIMESTAMP;
ALTER TABLE two_factor_challenges ADD COLUMN session_expires_at TIMESTAMP;

-- Sessions open at upgrade have no recorded login time: open sessions of
-- OIDC-linked accounts end 12 hours after the upgrade. The column has no time
-- zone and the driver reads it as UTC, so the deadline is computed in UTC
-- whatever the database's TimeZone setting.
UPDATE refresh_tokens SET session_expires_at = (CURRENT_TIMESTAMP AT TIME ZONE 'UTC') + INTERVAL '12 hours'
WHERE revoked_at IS NULL
  AND user_id IN (SELECT id FROM users WHERE oidc_provider <> '');
