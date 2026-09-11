CREATE TABLE catalog_episode_spans (
 catalog_id TEXT PRIMARY KEY REFERENCES catalog_items(id) ON DELETE CASCADE,
 source_season INTEGER NOT NULL,
 source_start INTEGER NOT NULL,
 source_end INTEGER NOT NULL,
 absolute_episode INTEGER NOT NULL DEFAULT 0,
 CHECK(source_season >= 0 AND source_start > 0 AND source_end >= source_start)
);
CREATE TABLE catalog_episode_orders (
 series_id TEXT PRIMARY KEY REFERENCES catalog_series(id) ON DELETE CASCADE,
 order_kind TEXT NOT NULL CHECK(order_kind IN ('aired','dvd','absolute')),
 revision INTEGER NOT NULL DEFAULT 0,
 needs_repair INTEGER NOT NULL DEFAULT 0 CHECK(needs_repair IN (0,1))
);
CREATE TABLE catalog_episode_order_items (
 series_id TEXT NOT NULL REFERENCES catalog_series(id) ON DELETE CASCADE,
 catalog_id TEXT NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
 position INTEGER NOT NULL,
 end_position INTEGER NOT NULL,
 display_season INTEGER NOT NULL,
 display_episode INTEGER NOT NULL,
 display_episode_end INTEGER NOT NULL,
 special INTEGER NOT NULL DEFAULT 0 CHECK(special IN (0,1)),
 PRIMARY KEY(series_id,catalog_id),
 CHECK(position > 0 AND end_position >= position AND display_season >= 0 AND display_episode > 0 AND display_episode_end >= display_episode)
);
CREATE INDEX catalog_episode_order_sequence ON catalog_episode_order_items(series_id,position,end_position,catalog_id);
