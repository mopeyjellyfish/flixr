DROP INDEX catalog_fingerprint;
CREATE INDEX catalog_fingerprint ON catalog_items(root_kind,fingerprint);
-- catalog_items keeps its existing ID as the durable logical title anchor.
-- Physical sources are private scanner state; catalog_items retains the current
-- primary-source projection for existing readers until they migrate to this table.
CREATE TABLE catalog_physical_files (
 id TEXT PRIMARY KEY,
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE RESTRICT,
 root_kind TEXT NOT NULL CHECK(root_kind IN ('film','episode')),
 relative_path TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 full_digest TEXT NOT NULL DEFAULT '',
 change_token TEXT NOT NULL DEFAULT '',
 source_series_id TEXT NOT NULL DEFAULT '',
 size_bytes INTEGER NOT NULL DEFAULT 0,
 mtime_unix INTEGER NOT NULL DEFAULT 0,
 last_seen INTEGER NOT NULL DEFAULT 0,
 present INTEGER NOT NULL DEFAULT 1 CHECK(present IN (0,1)),
 selected INTEGER NOT NULL DEFAULT 0 CHECK(selected IN (0,1))
);
CREATE UNIQUE INDEX catalog_physical_location ON catalog_physical_files(root_kind,relative_path) WHERE present=1;
CREATE INDEX catalog_physical_files_catalog ON catalog_physical_files(catalog_id, present, selected);
CREATE INDEX catalog_physical_files_fingerprint ON catalog_physical_files(root_kind, fingerprint, size_bytes);

ALTER TABLE catalog_items ADD COLUMN primary_file_id TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN available INTEGER NOT NULL DEFAULT 1 CHECK(available IN (0,1));

INSERT INTO catalog_physical_files(id,catalog_id,root_kind,relative_path,fingerprint,size_bytes,mtime_unix,source_series_id,present,selected)
 SELECT id || ':legacy',id,root_kind,relative_path,fingerprint,size_bytes,mtime_unix,series_id,1,1
 FROM catalog_items;
UPDATE catalog_items SET primary_file_id=id || ':legacy' WHERE primary_file_id='';

CREATE TABLE catalog_identity_conflicts (
 id INTEGER PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('film','episode','series')),
 reason TEXT NOT NULL CHECK(reason IN ('provider_identity','replacement_evidence','ambiguous_duplicate')),
 left_catalog_id TEXT NOT NULL,
 right_catalog_id TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'open' CHECK(state IN ('open','merged','unmerged','dismissed')),
 created_at INTEGER NOT NULL,
 resolved_at INTEGER NOT NULL DEFAULT 0,
 CHECK(left_catalog_id < right_catalog_id)
);

CREATE UNIQUE INDEX catalog_identity_open_conflict ON catalog_identity_conflicts(kind,reason,left_catalog_id,right_catalog_id) WHERE state='open';

CREATE TABLE catalog_identity_merges (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('film','episode','series')),
 survivor_catalog_id TEXT NOT NULL,
 source_catalog_id TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','unmerged','conflicted')),
 created_at INTEGER NOT NULL,
 unmerged_at INTEGER NOT NULL DEFAULT 0,
 CHECK(survivor_catalog_id <> source_catalog_id)
);
CREATE UNIQUE INDEX catalog_identity_active_source ON catalog_identity_merges(source_catalog_id) WHERE state='active';

CREATE TABLE catalog_identity_progress_snapshots (
 merge_id TEXT NOT NULL REFERENCES catalog_identity_merges(id) ON DELETE CASCADE,
 phase TEXT NOT NULL CHECK(phase IN ('before','after')),
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL,
 position_ms INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 completed INTEGER NOT NULL,
 completed_at INTEGER NOT NULL,
 generation INTEGER NOT NULL,
 observation INTEGER NOT NULL,
 completion_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(merge_id,phase,profile_id,catalog_id)
);

ALTER TABLE catalog_items ADD COLUMN merged_into TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_series ADD COLUMN merged_into TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_identity_merges ADD COLUMN decisions_json TEXT NOT NULL DEFAULT '[]';
CREATE TABLE catalog_identity_episode_snapshots (
 merge_id TEXT NOT NULL REFERENCES catalog_identity_merges(id),
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id),
 series_id TEXT NOT NULL,
 season_id TEXT NOT NULL,
 PRIMARY KEY(merge_id,catalog_id)
);
