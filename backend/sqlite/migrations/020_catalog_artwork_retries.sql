CREATE TABLE catalog_artwork_retries (
    catalog_kind TEXT NOT NULL CHECK (catalog_kind IN ('film', 'series', 'episode')),
    catalog_id TEXT NOT NULL,
    artwork_kind TEXT NOT NULL CHECK (artwork_kind IN ('poster', 'backdrop')),
    provider_id TEXT NOT NULL CHECK (provider_id <> ''),
    parent_catalog_id TEXT NOT NULL DEFAULT '',
    parent_provider_id TEXT NOT NULL DEFAULT '',
    provider_path TEXT NOT NULL CHECK (provider_path <> ''),
    PRIMARY KEY (catalog_kind, catalog_id, artwork_kind)
);
