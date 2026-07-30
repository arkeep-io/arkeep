-- The Restic repository password normally lives on the policy. Snapshots
-- imported from a pre-existing repository have no policy, so browsing and
-- restoring them needs the password stored on the destination itself.
-- Encrypted at rest by the EncryptedString type, like destinations.credentials.
ALTER TABLE destinations ADD COLUMN repo_password TEXT NOT NULL DEFAULT '';
