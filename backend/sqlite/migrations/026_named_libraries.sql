CREATE TABLE libraries (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('film','episode')),
 created_at INTEGER NOT NULL DEFAULT 0
);

ALTER TABLE library_removal_candidates RENAME TO library_removal_candidates_legacy;
ALTER TABLE library_locations RENAME TO library_locations_legacy;

CREATE TABLE library_locations (
 id TEXT PRIMARY KEY,
 library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
 root_path TEXT NOT NULL UNIQUE,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
 state TEXT NOT NULL DEFAULT 'unknown' CHECK(state IN ('unknown','available','unavailable','review_required')),
 scan_complete INTEGER NOT NULL DEFAULT 0 CHECK(scan_complete IN (0,1)),
 item_count INTEGER NOT NULL DEFAULT 0 CHECK(item_count >= 0),
 missing_count INTEGER NOT NULL DEFAULT 0 CHECK(missing_count >= 0),
 pending_scan_id TEXT NOT NULL DEFAULT '',
 last_scan_id TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX library_locations_library ON library_locations(library_id,id);

CREATE TABLE library_removal_candidates (
 location_id TEXT NOT NULL REFERENCES library_locations(id) ON DELETE CASCADE,
 scan_id TEXT NOT NULL,
 physical_file_id TEXT NOT NULL REFERENCES catalog_physical_files(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 PRIMARY KEY(location_id,scan_id,physical_file_id)
);

INSERT INTO libraries(id,name,kind) VALUES
 ('films','Films','film'),
 ('tv','TV','episode');

INSERT INTO library_locations(id,library_id,root_path)
 SELECT 'films-root','films',value FROM settings WHERE key='film_root' AND value<>'';
INSERT INTO library_locations(id,library_id,root_path)
 SELECT 'tv-root','tv',value FROM settings WHERE key='tv_root' AND value<>'';

UPDATE library_locations SET
 state=COALESCE((SELECT state FROM library_locations_legacy WHERE root_kind='film'),'unknown'),
 scan_complete=COALESCE((SELECT scan_complete FROM library_locations_legacy WHERE root_kind='film'),0),
 item_count=COALESCE((SELECT item_count FROM library_locations_legacy WHERE root_kind='film'),0),
 missing_count=COALESCE((SELECT missing_count FROM library_locations_legacy WHERE root_kind='film'),0),
 pending_scan_id=COALESCE((SELECT pending_scan_id FROM library_locations_legacy WHERE root_kind='film'),''),
 last_scan_id=COALESCE((SELECT last_scan_id FROM library_locations_legacy WHERE root_kind='film'),''),
 updated_at=COALESCE((SELECT updated_at FROM library_locations_legacy WHERE root_kind='film'),0),
 message=COALESCE((SELECT message FROM library_locations_legacy WHERE root_kind='film'),'')
 WHERE id='films-root';
UPDATE library_locations SET
 state=COALESCE((SELECT state FROM library_locations_legacy WHERE root_kind='episode'),'unknown'),
 scan_complete=COALESCE((SELECT scan_complete FROM library_locations_legacy WHERE root_kind='episode'),0),
 item_count=COALESCE((SELECT item_count FROM library_locations_legacy WHERE root_kind='episode'),0),
 missing_count=COALESCE((SELECT missing_count FROM library_locations_legacy WHERE root_kind='episode'),0),
 pending_scan_id=COALESCE((SELECT pending_scan_id FROM library_locations_legacy WHERE root_kind='episode'),''),
 last_scan_id=COALESCE((SELECT last_scan_id FROM library_locations_legacy WHERE root_kind='episode'),''),
 updated_at=COALESCE((SELECT updated_at FROM library_locations_legacy WHERE root_kind='episode'),0),
 message=COALESCE((SELECT message FROM library_locations_legacy WHERE root_kind='episode'),'')
 WHERE id='tv-root';

INSERT INTO library_removal_candidates(location_id,scan_id,physical_file_id,catalog_id)
 SELECT CASE root_kind WHEN 'film' THEN 'films-root' ELSE 'tv-root' END,scan_id,physical_file_id,catalog_id
 FROM library_removal_candidates_legacy
 WHERE EXISTS (
  SELECT 1 FROM library_locations
  WHERE id=CASE root_kind WHEN 'film' THEN 'films-root' ELSE 'tv-root' END
 );

DROP TABLE library_removal_candidates_legacy;
DROP TABLE library_locations_legacy;

ALTER TABLE catalog_physical_files ADD COLUMN location_id TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN source_location_id TEXT NOT NULL DEFAULT '';
UPDATE catalog_physical_files SET location_id=CASE root_kind WHEN 'film' THEN 'films-root' ELSE 'tv-root' END;
UPDATE catalog_items SET source_location_id=CASE root_kind WHEN 'film' THEN 'films-root' ELSE 'tv-root' END;
DROP INDEX catalog_physical_location;
CREATE UNIQUE INDEX catalog_physical_location ON catalog_physical_files(location_id,relative_path) WHERE present=1;
CREATE INDEX catalog_physical_location_catalog ON catalog_physical_files(location_id,catalog_id,present);

CREATE TABLE library_location_removal_previews (
 id TEXT PRIMARY KEY,
 location_id TEXT NOT NULL REFERENCES library_locations(id) ON DELETE CASCADE,
 location_revision INTEGER NOT NULL,
 new_root_path TEXT NOT NULL DEFAULT '',
 source_count INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL
);
CREATE TABLE library_location_removal_preview_sources (
 preview_id TEXT NOT NULL REFERENCES library_location_removal_previews(id) ON DELETE CASCADE,
 physical_file_id TEXT NOT NULL REFERENCES catalog_physical_files(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 PRIMARY KEY(preview_id,physical_file_id)
);
