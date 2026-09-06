CREATE TABLE IF NOT EXISTS catalog_artwork (
 catalog_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('poster','backdrop')),
 content_type TEXT NOT NULL,
 PRIMARY KEY(catalog_id, kind)
);
