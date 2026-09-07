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

-- Databases upgraded from an older release can contain a provider match whose
-- offered artwork failed before retry paths were durable. Seed a bounded queue
-- from only those pre-020 identities; new matches write the retry table directly.
CREATE TABLE catalog_artwork_reconciliations (
    catalog_kind TEXT NOT NULL CHECK (catalog_kind IN ('film', 'series', 'episode')),
    catalog_id TEXT NOT NULL,
    provider_id TEXT NOT NULL CHECK (provider_id <> ''),
    parent_catalog_id TEXT NOT NULL DEFAULT '',
    parent_provider_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (catalog_kind, catalog_id)
);

INSERT INTO catalog_artwork_reconciliations(catalog_kind,catalog_id,provider_id)
SELECT 'film',id,provider_id FROM catalog_items
WHERE kind='film' AND provider_id<>'' AND owner_unmatched=0 AND (poster='' OR backdrop='');

INSERT INTO catalog_artwork_reconciliations(catalog_kind,catalog_id,provider_id)
SELECT 'series',id,provider_id FROM catalog_series
WHERE provider_id<>'' AND owner_unmatched=0 AND (poster='' OR backdrop='');

INSERT INTO catalog_artwork_reconciliations(catalog_kind,catalog_id,provider_id,parent_catalog_id,parent_provider_id)
SELECT 'episode',catalog_items.id,catalog_items.provider_id,catalog_items.series_id,catalog_series.provider_id
FROM catalog_items JOIN catalog_series ON catalog_series.id=catalog_items.series_id
WHERE catalog_items.kind='episode' AND catalog_items.provider_id<>'' AND catalog_series.provider_id<>''
  AND (catalog_items.poster='' OR catalog_items.backdrop='');
