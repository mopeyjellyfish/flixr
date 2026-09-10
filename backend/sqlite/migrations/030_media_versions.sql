ALTER TABLE catalog_physical_files ADD COLUMN container TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_physical_files ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_physical_files ADD COLUMN video_codec TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_physical_files ADD COLUMN video_profile TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_physical_files ADD COLUMN video_level INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_physical_files ADD COLUMN primary_video_stream_index INTEGER NOT NULL DEFAULT -1;
ALTER TABLE catalog_physical_files ADD COLUMN video_width INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_physical_files ADD COLUMN video_height INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_physical_files ADD COLUMN video_bitrate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_physical_files ADD COLUMN video_frame_rate_milli INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_physical_files ADD COLUMN video_bit_depth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_physical_files ADD COLUMN video_hdr TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_physical_files ADD COLUMN audio_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE catalog_physical_files ADD COLUMN subtitle_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE catalog_physical_files ADD COLUMN probe_revision INTEGER NOT NULL DEFAULT 0;

UPDATE catalog_physical_files
SET (container,duration_ms,video_codec,video_profile,video_level,primary_video_stream_index,
     video_width,video_height,video_bitrate,video_frame_rate_milli,video_bit_depth,video_hdr,
     audio_json,subtitle_json,probe_revision) =
    (SELECT container,duration_ms,video_codec,video_profile,video_level,primary_video_stream_index,
            video_width,video_height,video_bitrate,video_frame_rate_milli,video_bit_depth,video_hdr,
            audio_json,subtitle_json,probe_revision
     FROM catalog_items WHERE catalog_items.id=catalog_physical_files.catalog_id);

CREATE TABLE catalog_film_version_memberships (
 canonical_catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 member_catalog_id TEXT PRIMARY KEY REFERENCES catalog_items(id) ON DELETE CASCADE,
 created_at INTEGER NOT NULL,
 CHECK(canonical_catalog_id<>member_catalog_id)
);
CREATE INDEX catalog_film_version_canonical ON catalog_film_version_memberships(canonical_catalog_id,member_catalog_id);

CREATE TABLE catalog_series_version_memberships (
 canonical_series_id TEXT NOT NULL REFERENCES catalog_series(id) ON DELETE CASCADE,
 member_series_id TEXT PRIMARY KEY REFERENCES catalog_series(id) ON DELETE CASCADE,
 created_at INTEGER NOT NULL,
 CHECK(canonical_series_id<>member_series_id)
);
CREATE INDEX catalog_series_version_canonical ON catalog_series_version_memberships(canonical_series_id,member_series_id);

CREATE TABLE catalog_edition_labels (
 kind TEXT NOT NULL CHECK(kind IN ('film','series')),
 catalog_id TEXT NOT NULL,
 label TEXT NOT NULL CHECK(length(label)<=80),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(kind,catalog_id)
);

CREATE TABLE profile_media_version_preferences (
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('film','series')),
 catalog_id TEXT NOT NULL,
 version_id TEXT NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(profile_id,kind,catalog_id)
);
