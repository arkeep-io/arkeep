-- Migration: 000003_oidc_multi_provider
-- Removes redirect_url from oidc_providers: the callback URL is now computed
-- server-side as {base_url}/api/v1/auth/oidc/callback and never stored in DB.
-- Multiple providers are already supported by the table structure; this migration
-- only cleans up the column that is no longer managed by users.
ALTER TABLE oidc_providers DROP COLUMN redirect_url;
