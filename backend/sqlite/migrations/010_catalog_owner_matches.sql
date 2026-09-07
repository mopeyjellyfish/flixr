ALTER TABLE catalog_items ADD COLUMN metadata_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN metadata_language TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN metadata_region TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN match_confidence REAL NOT NULL DEFAULT 0;
ALTER TABLE catalog_items ADD COLUMN owner_matched INTEGER NOT NULL DEFAULT 0;

ALTER TABLE catalog_series ADD COLUMN metadata_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_series ADD COLUMN metadata_language TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_series ADD COLUMN metadata_region TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_series ADD COLUMN match_confidence REAL NOT NULL DEFAULT 0;
ALTER TABLE catalog_series ADD COLUMN owner_matched INTEGER NOT NULL DEFAULT 0;
