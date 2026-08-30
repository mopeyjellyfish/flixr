ALTER TABLE catalog_items ADD COLUMN video_profile TEXT NOT NULL DEFAULT '';

UPDATE catalog_items SET container = 'matroska' WHERE lower(relative_path) LIKE '%.mkv';
UPDATE catalog_items SET container = 'webm' WHERE lower(relative_path) LIKE '%.webm';
