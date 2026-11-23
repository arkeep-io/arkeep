ALTER TABLE agents DROP COLUMN repair_available;
ALTER TABLE destinations DROP COLUMN maintenance_job_id;
ALTER TABLE destinations DROP COLUMN maintenance_by;
ALTER TABLE destinations DROP COLUMN maintenance_since;
ALTER TABLE jobs DROP COLUMN requested_by;
ALTER TABLE jobs DROP COLUMN params;
