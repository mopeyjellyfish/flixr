CREATE TABLE library_locations (
 root_kind TEXT PRIMARY KEY CHECK(root_kind IN ('film','episode')),
 root_path TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'unknown' CHECK(state IN ('unknown','available','unavailable','review_required')),
 scan_complete INTEGER NOT NULL DEFAULT 0 CHECK(scan_complete IN (0,1)),
 item_count INTEGER NOT NULL DEFAULT 0 CHECK(item_count >= 0),
 missing_count INTEGER NOT NULL DEFAULT 0 CHECK(missing_count >= 0),
 pending_scan_id TEXT NOT NULL DEFAULT '',
 last_scan_id TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT ''
);

CREATE TABLE library_removal_candidates (
 root_kind TEXT NOT NULL CHECK(root_kind IN ('film','episode')),
 scan_id TEXT NOT NULL,
 physical_file_id TEXT NOT NULL,
 catalog_id TEXT NOT NULL,
 PRIMARY KEY(root_kind,scan_id,physical_file_id),
 FOREIGN KEY(root_kind) REFERENCES library_locations(root_kind) ON DELETE CASCADE,
 FOREIGN KEY(physical_file_id) REFERENCES catalog_physical_files(id) ON DELETE CASCADE,
 FOREIGN KEY(catalog_id) REFERENCES catalog_items(id) ON DELETE CASCADE
);
