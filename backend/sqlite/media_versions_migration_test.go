package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestMediaVersionsMigrationBackfillsPhysicalPropertiesAndPreservesCatalog(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", databaseDSN(filepath.Join(dir, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var version int
		if _, err = fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 30 {
			continue
		}
		body, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = legacy.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", version, err)
		}
		if _, err = legacy.Exec(`INSERT INTO schema_migrations VALUES(?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec(`INSERT INTO library_locations(id,library_id,root_path) VALUES('fixture-root','films','/media')`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,source_location_id,container,duration_ms,video_codec,video_profile,video_level,primary_video_stream_index,video_width,video_height,video_bitrate,video_frame_rate_milli,video_bit_depth,video_hdr,audio_json,subtitle_json,probe_revision) VALUES('film','film','Preserved','film.mkv','film','fixture-root','matroska',90000,'hevc','Main',51,0,3840,2160,20000000,24000,10,'smpte2084','[{"index":1,"codec":"aac"}]','[{"index":2,"codec":"subrip"}]',3)`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO catalog_physical_files(id,catalog_id,location_id,root_kind,relative_path,fingerprint,full_digest,present,selected) VALUES('physical','film','fixture-root','film','film.mkv','fingerprint','digest',1,1)`); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var title, container, codec, hdr, audio, subtitles string
	var width, height, revision int
	if err := db.QueryRow(`SELECT i.title,f.container,f.video_codec,f.video_width,f.video_height,f.video_hdr,f.audio_json,f.subtitle_json,f.probe_revision FROM catalog_items i JOIN catalog_physical_files f ON f.catalog_id=i.id WHERE i.id='film'`).Scan(&title, &container, &codec, &width, &height, &hdr, &audio, &subtitles, &revision); err != nil {
		t.Fatal(err)
	}
	if title != "Preserved" || container != "matroska" || codec != "hevc" || width != 3840 || height != 2160 || hdr != "smpte2084" || audio != `[{"index":1,"codec":"aac"}]` || subtitles != `[{"index":2,"codec":"subrip"}]` || revision != 3 {
		t.Fatalf("backfill title=%q container=%q codec=%q dimensions=%dx%d hdr=%q audio=%q subtitles=%q revision=%d", title, container, codec, width, height, hdr, audio, subtitles, revision)
	}
}
