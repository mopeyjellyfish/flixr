CREATE TABLE catalog_metadata_fields (
 catalog_kind TEXT NOT NULL CHECK(catalog_kind IN ('film','series')),
 catalog_id TEXT NOT NULL,
 field TEXT NOT NULL CHECK(field IN ('title','synopsis','year','poster','backdrop','tags','content_rating')),
 value TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL CHECK(source IN ('provider','owner','local')),
 locked INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(catalog_kind,catalog_id,field)
);
CREATE INDEX catalog_metadata_fields_target ON catalog_metadata_fields(catalog_kind,catalog_id);
