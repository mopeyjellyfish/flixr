ALTER TABLE catalog_metadata_fields RENAME TO catalog_metadata_fields_old;

CREATE TABLE catalog_metadata_fields (
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series','episode')),
 catalog_id TEXT NOT NULL,
 field TEXT NOT NULL CHECK(field IN ('title','synopsis','year','poster','backdrop','tags','content_rating')),
 value TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL CHECK(source IN ('provider','owner','local')),
 locked INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(catalog_kind,catalog_id,field)
);
INSERT INTO catalog_metadata_fields SELECT * FROM catalog_metadata_fields_old;
DROP TABLE catalog_metadata_fields_old;
CREATE INDEX catalog_metadata_fields_target ON catalog_metadata_fields(catalog_kind,catalog_id);

CREATE TABLE catalog_local_metadata (
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series','episode')),
 catalog_id TEXT NOT NULL,
 location_id TEXT NOT NULL,
 relative_path TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 fields_json TEXT NOT NULL DEFAULT '{}',
 fallback_json TEXT NOT NULL DEFAULT '{}',
 provider_id TEXT NOT NULL DEFAULT '',
 fallback_provider_id TEXT NOT NULL DEFAULT '',
 fallback_provider TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(catalog_kind,catalog_id)
);

CREATE TABLE catalog_local_artwork (
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series','episode')),
 catalog_id TEXT NOT NULL,
 artwork_kind TEXT NOT NULL CHECK(artwork_kind IN ('poster','backdrop')),
 location_id TEXT NOT NULL,
 relative_path TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 local_object_name TEXT NOT NULL,
 fallback_object_name TEXT NOT NULL DEFAULT '',
 fallback_content_type TEXT NOT NULL DEFAULT '',
 fallback_value TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(catalog_kind,catalog_id,artwork_kind)
);

CREATE TABLE catalog_local_identity_artwork (
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series','episode')),
 catalog_id TEXT NOT NULL,
 artwork_kind TEXT NOT NULL CHECK(artwork_kind IN ('poster','backdrop')),
 object_name TEXT NOT NULL,
 content_type TEXT NOT NULL,
 value TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(catalog_kind,catalog_id,artwork_kind)
);

CREATE TABLE catalog_local_identity_state (
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series','episode')),
 catalog_id TEXT NOT NULL,
 provider_id TEXT NOT NULL DEFAULT '',
 provider TEXT NOT NULL DEFAULT '',
 fields_json TEXT NOT NULL DEFAULT '{}',
 PRIMARY KEY(catalog_kind,catalog_id)
);
