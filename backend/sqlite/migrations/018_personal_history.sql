CREATE TABLE profile_ratings (
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL,
 rating INTEGER NOT NULL CHECK(rating BETWEEN 1 AND 5),
 provenance TEXT NOT NULL CHECK(provenance IN ('local','import')),
 source_id TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(profile_id,catalog_id)
);
CREATE TABLE viewing_events (
 event_id TEXT PRIMARY KEY,
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL,
 title TEXT NOT NULL,
 kind TEXT NOT NULL,
 event_type TEXT NOT NULL CHECK(event_type IN ('completed','summary')),
 provenance TEXT NOT NULL CHECK(provenance IN ('local','import')),
 source_id TEXT NOT NULL,
 source_time INTEGER,
 recorded_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX viewing_event_source ON viewing_events(profile_id, provenance, catalog_id, source_id) WHERE source_id <> '';
CREATE INDEX viewing_event_history ON viewing_events(profile_id, recorded_at DESC, event_id DESC);
CREATE TABLE viewing_history_clears (
 clear_id TEXT PRIMARY KEY,
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 cleared_at INTEGER NOT NULL,
 cleared_rowid INTEGER NOT NULL DEFAULT 0,
 undo_until INTEGER NOT NULL,
 undone_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX viewing_history_clears_profile ON viewing_history_clears(profile_id, cleared_at DESC);
ALTER TABLE progress ADD COLUMN completion_id TEXT NOT NULL DEFAULT '';
