CREATE TABLE profile_continue_watching_dismissals (
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series')),
 catalog_id TEXT NOT NULL,
 dismissed_at INTEGER NOT NULL,
 PRIMARY KEY(profile_id,catalog_kind,catalog_id)
);

CREATE TABLE catalog_identity_continue_watching_snapshots (
 merge_id TEXT NOT NULL REFERENCES catalog_identity_merges(id) ON DELETE CASCADE,
 phase TEXT NOT NULL CHECK(phase IN ('before','after')),
 profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series')),
 catalog_id TEXT NOT NULL,
 dismissed_at INTEGER NOT NULL,
 PRIMARY KEY(merge_id,phase,profile_id,catalog_kind,catalog_id)
);
