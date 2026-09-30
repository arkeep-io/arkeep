-- Group-based access control for OIDC providers (#277).
--
-- groups_claim names the ID token / UserInfo claim carrying the user's groups.
-- allowed_groups and admin_groups are comma-separated group names: when
-- allowed_groups is set only its members may sign in, and when admin_groups is
-- set the role is synced on every login (admin for its members, user otherwise).
ALTER TABLE oidc_providers ADD COLUMN groups_claim TEXT NOT NULL DEFAULT 'groups';
ALTER TABLE oidc_providers ADD COLUMN allowed_groups TEXT NOT NULL DEFAULT '';
ALTER TABLE oidc_providers ADD COLUMN admin_groups TEXT NOT NULL DEFAULT '';
