-- Issue #289: deleting a destination now wipes its stored secrets. Apply the
-- same to destinations soft-deleted before this release, whose credentials and
-- repository password were kept in the database with nothing left using them.
UPDATE destinations SET credentials = '', repo_password = '' WHERE deleted_at IS NOT NULL;
