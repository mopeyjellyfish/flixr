CREATE TABLE profile_audio_preferences (
 profile_id TEXT PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
 language TEXT NOT NULL
);

CREATE TABLE catalog_audio_sidecars (
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 selection_index INTEGER NOT NULL,
 source_stream_index INTEGER NOT NULL,
 relative_path TEXT NOT NULL,
 codec TEXT NOT NULL,
 channels INTEGER NOT NULL DEFAULT 0,
 language TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 is_default INTEGER NOT NULL DEFAULT 0,
 is_forced INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(catalog_id, selection_index)
);
