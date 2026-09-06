ALTER TABLE catalog_items ADD COLUMN fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN container TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN video_codec TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN audio_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE catalog_items ADD COLUMN subtitle_json TEXT NOT NULL DEFAULT '[]';
CREATE UNIQUE INDEX IF NOT EXISTS catalog_fingerprint ON catalog_items(root_kind, fingerprint) WHERE fingerprint <> '';
CREATE INDEX IF NOT EXISTS catalog_browse ON catalog_items(title COLLATE NOCASE, id);
CREATE INDEX IF NOT EXISTS catalog_search ON catalog_items(title COLLATE NOCASE);
CREATE TABLE IF NOT EXISTS scan_files (scan_id TEXT NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE, relative_path TEXT NOT NULL, outcome TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', PRIMARY KEY(scan_id, relative_path));
