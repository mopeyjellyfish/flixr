CREATE TABLE profile_subtitle_preferences (
 profile_id TEXT PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
 mode TEXT NOT NULL DEFAULT 'automatic' CHECK(mode IN ('off','automatic')),
 language TEXT NOT NULL DEFAULT '',
 prefer_sdh INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE catalog_subtitle_sidecars (
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 selection_index INTEGER NOT NULL,
 source_stream_index INTEGER NOT NULL,
 relative_path TEXT NOT NULL,
 codec TEXT NOT NULL,
 language TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 is_default INTEGER NOT NULL DEFAULT 0,
 is_forced INTEGER NOT NULL DEFAULT 0,
 is_sdh INTEGER NOT NULL DEFAULT 0,
 size_bytes INTEGER NOT NULL,
 mtime_unix INTEGER NOT NULL,
 full_digest TEXT NOT NULL,
 change_token TEXT NOT NULL,
 PRIMARY KEY(catalog_id, selection_index)
);
