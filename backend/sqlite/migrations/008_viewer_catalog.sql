ALTER TABLE catalog_items ADD COLUMN genres_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE catalog_items ADD COLUMN added_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_items ADD COLUMN playable INTEGER NOT NULL DEFAULT 1;
ALTER TABLE catalog_items ADD COLUMN demo INTEGER NOT NULL DEFAULT 0;
UPDATE catalog_items SET added_at=updated_at WHERE added_at=0;

ALTER TABLE catalog_series ADD COLUMN genres_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE catalog_series ADD COLUMN added_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_series ADD COLUMN playable INTEGER NOT NULL DEFAULT 1;
ALTER TABLE catalog_series ADD COLUMN demo INTEGER NOT NULL DEFAULT 0;
UPDATE catalog_series SET added_at=COALESCE((SELECT MIN(catalog_items.updated_at) FROM catalog_items WHERE catalog_items.series_id=catalog_series.id), updated_at) WHERE added_at=0;

ALTER TABLE progress ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
UPDATE progress SET updated_at=0 WHERE updated_at IS NULL;

CREATE TABLE profile_film_list (
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 added_at INTEGER NOT NULL,
 PRIMARY KEY(profile_id, catalog_id)
);
CREATE TABLE profile_series_list (
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL REFERENCES catalog_series(id) ON DELETE CASCADE,
 added_at INTEGER NOT NULL,
 PRIMARY KEY(profile_id, catalog_id)
);
CREATE TABLE profile_view_preferences (
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 media TEXT NOT NULL CHECK(media IN ('all','film','series')),
 view_mode TEXT NOT NULL CHECK(view_mode IN ('rows','grid')),
 sort_mode TEXT NOT NULL CHECK(sort_mode IN ('title','year','added','watched')),
 PRIMARY KEY(profile_id, media)
);
CREATE INDEX catalog_items_browse_year ON catalog_items(year DESC, title COLLATE NOCASE, id);
CREATE INDEX catalog_items_browse_added ON catalog_items(added_at DESC, id);
CREATE INDEX catalog_series_browse_year ON catalog_series(year DESC, title COLLATE NOCASE, id);
CREATE INDEX catalog_series_browse_added ON catalog_series(added_at DESC, id);
CREATE INDEX progress_profile_updated ON progress(profile_id, updated_at DESC, catalog_id);
