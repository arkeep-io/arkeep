-- Migration: 000044_snapshot_summary
-- The statistics restic records about each snapshot (issue #470): when the
-- backup ran, how its files compare with the parent snapshot, how much it read
-- and how much it added before compression. size_bytes (data_added_packed) and
-- file_count (total_files_processed) already hold the rest of the summary.
--
-- backup_end is NULL while the summary is unknown: for records written before
-- this migration, until the next snapshot sync fills them from the repository,
-- and for snapshots made by restic < 0.17, which record no summary.
ALTER TABLE snapshots ADD COLUMN backup_start TIMESTAMP;
ALTER TABLE snapshots ADD COLUMN backup_end TIMESTAMP;
ALTER TABLE snapshots ADD COLUMN files_new BIGINT NOT NULL DEFAULT 0;
ALTER TABLE snapshots ADD COLUMN files_changed BIGINT NOT NULL DEFAULT 0;
ALTER TABLE snapshots ADD COLUMN files_unmodified BIGINT NOT NULL DEFAULT 0;
ALTER TABLE snapshots ADD COLUMN total_bytes_processed BIGINT NOT NULL DEFAULT 0;
ALTER TABLE snapshots ADD COLUMN data_added BIGINT NOT NULL DEFAULT 0;
