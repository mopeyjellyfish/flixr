ALTER TABLE catalog_items ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_items ADD COLUMN mtime_unix INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS catalog_file_state ON catalog_items(root_kind, relative_path, size_bytes, mtime_unix);
