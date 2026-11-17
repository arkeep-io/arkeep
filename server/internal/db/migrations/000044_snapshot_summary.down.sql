ALTER TABLE snapshots DROP COLUMN data_added;
ALTER TABLE snapshots DROP COLUMN total_bytes_processed;
ALTER TABLE snapshots DROP COLUMN files_unmodified;
ALTER TABLE snapshots DROP COLUMN files_changed;
ALTER TABLE snapshots DROP COLUMN files_new;
ALTER TABLE snapshots DROP COLUMN backup_end;
ALTER TABLE snapshots DROP COLUMN backup_start;
