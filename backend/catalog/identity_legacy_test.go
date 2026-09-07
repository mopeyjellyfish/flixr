package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestActualPre019LibraryPlaysBeforeRescan(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	content := []byte("unchanged legacy movie")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := contentFingerprint(file)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", filepath.Join(data, "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../sqlite/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		var version int
		if _, err := fmt.Sscanf(filepath.Base(name), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 19 {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec("INSERT INTO schema_migrations VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,fingerprint,size_bytes,mtime_unix,probe_revision,container,video_codec) VALUES('legacy','film','Legacy','Film.mp4','film',?,?,?,?,'mp4','h264')`, fingerprint, info.Size(), info.ModTime().UnixNano(), mediaProbeRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("INSERT INTO settings(key,value) VALUES('film_root',?)", films); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("INSERT INTO profiles(id,name) VALUES('p','One'); INSERT INTO progress(profile_id,catalog_id,position_ms) VALUES('p','legacy',123)"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		t.Fatal("unchanged legacy admission reprobed media")
		return MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.PlaybackItemContext(ctx, "legacy"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled legacy admission: %v", err)
	}
	var unproved string
	if err := db.QueryRow("SELECT full_digest FROM catalog_physical_files WHERE catalog_id='legacy'").Scan(&unproved); err != nil || unproved != "" {
		t.Fatalf("cancelled admission persisted proof %q %v", unproved, err)
	}
	item, err := c.PlaybackItem("legacy")
	if err != nil {
		t.Fatal(err)
	}
	file, err = c.OpenSource(item.ID, item.SourceKey())
	if err != nil {
		t.Fatalf("migrated source cannot play before rescan: %v", err)
	}
	bytes, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(bytes) != string(content) {
		t.Fatalf("legacy media %q %v", bytes, err)
	}
	var digest string
	if err := db.QueryRow("SELECT full_digest FROM catalog_physical_files WHERE catalog_id='legacy'").Scan(&digest); err != nil || len(digest) != 64 {
		t.Fatalf("admitted legacy proof %q %v", digest, err)
	}
	var position int
	if err := db.QueryRow("SELECT position_ms FROM progress WHERE catalog_id='legacy'").Scan(&position); err != nil || position != 123 {
		t.Fatalf("legacy progress %d %v", position, err)
	}
	if err := os.WriteFile(path, []byte("changed legacy movie"), 0600); err != nil {
		t.Fatal(err)
	}
	if file, err := c.OpenSource(item.ID, item.SourceKey()); err == nil {
		file.Close()
		t.Fatal("hydrated legacy session changed source")
	}
}
