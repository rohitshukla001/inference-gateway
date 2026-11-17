-- Helps the sweeper find 'pending' rows that no worker ever claimed.
CREATE INDEX idx_jobs_pending_created ON jobs(created_at) WHERE status = 'pending';
