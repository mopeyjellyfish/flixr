ALTER TABLE catalog_items ADD COLUMN video_frame_rate_milli INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_items ADD COLUMN video_bit_depth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_items ADD COLUMN video_level INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_audio_sidecars ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_audio_sidecars ADD COLUMN sample_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_audio_sidecars ADD COLUMN bitrate INTEGER NOT NULL DEFAULT 0;
