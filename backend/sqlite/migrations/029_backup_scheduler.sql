CREATE TABLE backup_policy (
 id INTEGER PRIMARY KEY CHECK(id=1),
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 destination TEXT NOT NULL DEFAULT '',
 schedule_kind TEXT NOT NULL DEFAULT 'interval' CHECK(schedule_kind IN ('interval','daily')),
 interval_seconds INTEGER NOT NULL DEFAULT 86400 CHECK(interval_seconds BETWEEN 3600 AND 31536000),
 local_time TEXT NOT NULL DEFAULT '03:00',
 timezone TEXT NOT NULL DEFAULT 'UTC',
 retain_count INTEGER NOT NULL DEFAULT 7 CHECK(retain_count BETWEEN 1 AND 100),
 retain_age_seconds INTEGER NOT NULL DEFAULT 2592000 CHECK(retain_age_seconds BETWEEN 86400 AND 315360000),
 budget_bytes INTEGER NOT NULL DEFAULT 10737418240 CHECK(budget_bytes BETWEEN 1048576 AND 1099511627776),
 next_run_at INTEGER,
 last_verified_at INTEGER,
 last_verified_file TEXT NOT NULL DEFAULT '',
 last_status TEXT NOT NULL DEFAULT 'never' CHECK(last_status IN ('never','succeeded','failed','cancelled','interrupted')),
 last_message TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO backup_policy(id) VALUES(1);

CREATE TABLE backup_jobs (
 id TEXT PRIMARY KEY,
 trigger TEXT NOT NULL CHECK(trigger IN ('manual','schedule')),
 status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','cancelled','interrupted')),
 queued_at INTEGER NOT NULL,
 started_at INTEGER,
 finished_at INTEGER,
 archive_name TEXT NOT NULL DEFAULT '',
 size_bytes INTEGER NOT NULL DEFAULT 0 CHECK(size_bytes >= 0),
 cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN (0,1)),
 message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX backup_jobs_history ON backup_jobs(queued_at DESC,id DESC);
CREATE UNIQUE INDEX backup_jobs_one_active ON backup_jobs(status) WHERE status IN ('queued','running');

UPDATE backup_jobs SET status='interrupted',finished_at=unixepoch(),message='Interrupted by server restart.' WHERE status IN ('queued','running');
