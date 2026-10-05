-- Deleting a policy now wipes its stored repository password (follow-up to
-- issue #289). Apply the same to policies soft-deleted before this release.
UPDATE policies SET repo_password = '' WHERE deleted_at IS NOT NULL;
