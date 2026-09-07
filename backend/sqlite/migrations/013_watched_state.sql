CREATE TABLE IF NOT EXISTS progress (profile_id TEXT NOT NULL, catalog_id TEXT NOT NULL, position_ms INTEGER NOT NULL, updated_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(profile_id, catalog_id));
ALTER TABLE progress ADD COLUMN completed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE progress ADD COLUMN completed_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX progress_profile_completion ON progress(profile_id, completed, updated_at DESC, catalog_id);
