CREATE INDEX IF NOT EXISTS catalog_browse_films ON catalog_items(series_id, title COLLATE NOCASE, id);
