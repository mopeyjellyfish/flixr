ALTER TABLE scan_runs ADD COLUMN total INTEGER;
ALTER TABLE scan_runs ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;

CREATE TABLE library_scan_policies (
 library_id TEXT PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 schedule_kind TEXT NOT NULL DEFAULT 'interval' CHECK(schedule_kind IN ('interval','daily')),
 interval_seconds INTEGER NOT NULL DEFAULT 86400 CHECK(interval_seconds BETWEEN 60 AND 31536000),
 local_time TEXT NOT NULL DEFAULT '03:00',
 timezone TEXT NOT NULL DEFAULT 'UTC',
 next_run_at INTEGER,
 schedule_override TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO library_scan_policies(library_id) SELECT id FROM libraries;

CREATE TABLE library_scan_exclusions (
 library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
 pattern TEXT NOT NULL,
 PRIMARY KEY(library_id,pattern)
);

CREATE TABLE scan_jobs (
 id TEXT PRIMARY KEY,
 library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
 trigger TEXT NOT NULL CHECK(trigger IN ('manual','schedule','startup','retry')),
 status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','partial','failed','cancelled','interrupted')),
 queued_at INTEGER NOT NULL,
 started_at INTEGER,
 finished_at INTEGER,
 not_before INTEGER NOT NULL DEFAULT 0,
 attempt INTEGER NOT NULL DEFAULT 1 CHECK(attempt BETWEEN 1 AND 3),
 retry_of TEXT NOT NULL DEFAULT '',
 cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN (0,1)),
 total INTEGER,
 scanned INTEGER NOT NULL DEFAULT 0,
 skipped INTEGER NOT NULL DEFAULT 0,
 failed INTEGER NOT NULL DEFAULT 0,
 unmatched INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX scan_jobs_queue ON scan_jobs(status,not_before,queued_at,id);
CREATE INDEX scan_jobs_library ON scan_jobs(library_id,queued_at DESC,id DESC);
CREATE UNIQUE INDEX scan_jobs_one_active_library ON scan_jobs(library_id) WHERE status IN ('queued','running');

CREATE TABLE scan_job_files (
 job_id TEXT NOT NULL REFERENCES scan_jobs(id) ON DELETE CASCADE,
 location_id TEXT NOT NULL,
 relative_path TEXT NOT NULL,
 outcome TEXT NOT NULL,
 error_code TEXT NOT NULL DEFAULT '',
 message TEXT NOT NULL DEFAULT '',
 retryable INTEGER NOT NULL DEFAULT 0 CHECK(retryable IN (0,1)),
 PRIMARY KEY(job_id,location_id,relative_path)
);

UPDATE scan_runs SET status='interrupted',finished_at=unixepoch(),message='Interrupted by server restart.' WHERE status='running';
