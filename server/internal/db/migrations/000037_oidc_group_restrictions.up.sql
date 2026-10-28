-- Optional group-based access control per OIDC provider (issue #277).
-- groups_claim names the claim holding the user's groups; allowed_groups and
-- admin_groups are comma-separated group names. Empty allowed_groups and
-- admin_groups keep the previous behaviour: any user of the IdP may sign in.
ALTER TABLE oidc_providers ADD COLUMN groups_claim TEXT NOT NULL DEFAULT '';
ALTER TABLE oidc_providers ADD COLUMN allowed_groups TEXT NOT NULL DEFAULT '';
ALTER TABLE oidc_providers ADD COLUMN admin_groups TEXT NOT NULL DEFAULT '';
