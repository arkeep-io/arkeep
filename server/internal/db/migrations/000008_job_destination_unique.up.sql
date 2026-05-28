CREATE UNIQUE INDEX IF NOT EXISTS uq_job_destinations_job_dest
    ON job_destinations (job_id, destination_id);
