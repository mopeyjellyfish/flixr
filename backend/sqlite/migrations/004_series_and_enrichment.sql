CREATE TABLE IF NOT EXISTS catalog_series (
 id TEXT PRIMARY KEY,
 title TEXT NOT NULL,
 local_only INTEGER NOT NULL DEFAULT 1,
 provider_id TEXT NOT NULL DEFAULT '',
 year INTEGER NOT NULL DEFAULT 0,
 synopsis TEXT NOT NULL DEFAULT '',
 poster TEXT NOT NULL DEFAULT '',
 backdrop TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS catalog_seasons (
 id TEXT PRIMARY KEY,
 series_id TEXT NOT NULL REFERENCES catalog_series(id) ON DELETE CASCADE,
 number INTEGER NOT NULL,
 UNIQUE(series_id, number)
);
ALTER TABLE catalog_items ADD COLUMN series_id TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN season_id TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN year INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_items ADD COLUMN synopsis TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN poster TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN backdrop TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS catalog_episode_series ON catalog_items(series_id, season_id, id);
CREATE INDEX IF NOT EXISTS catalog_series_title ON catalog_series(title COLLATE NOCASE, id);
