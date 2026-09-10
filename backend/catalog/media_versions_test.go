package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func versionCatalogFixture(t *testing.T) (*Catalog, *sqlite.DB, string, string) {
	t.Helper()
	data, films, tv := t.TempDir(), t.TempDir(), t.TempDir()
	for name, body := range map[string]string{"Film 1080.mp4": "blue", "Film 4K.mp4": "red"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{
		"Show A/Season 01/Show A S01E01.mp4",
		"Show A/Season 01/Show A S01E02.mp4",
		"Show B/Season 01/Show B S01E01.mp4",
		"Show C/Season 01/Show C S01E01.mp4",
		"Show C/Season 01/Show C S01E02.mp4",
	} {
		path := filepath.Join(tv, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := OpenWithProber(db, ProberFunc(func(_ context.Context, file *os.File) (MediaProperties, error) {
		info, _ := file.Stat()
		width, height := 1920, 1080
		if info.Name() == "Film 4K.mp4" {
			width, height = 3840, 2160
		}
		return MediaProperties{Container: "mp4", VideoCodec: "h264", Width: width, Height: height, Bitrate: int64(width) * 1000}, nil
	}))
	if err != nil || c.SetRoots(films, tv) != nil || c.Scan(context.Background(), 1) != nil {
		db.Close()
		t.Fatalf("scan fixture: %v", err)
	}
	return c, db, films, tv
}

func TestMediaVersionGroupPreservesSeparateHistoryAcrossRestartAndUngroup(t *testing.T) {
	c, db, _, _ := versionCatalogFixture(t)
	defer db.Close()
	items, _, err := c.Browse("Film", 0, 0)
	if err != nil || len(items) != 2 {
		t.Fatalf("films = %#v, %v", items, err)
	}
	canonical, member := items[0].ID, items[1].ID
	if _, err := db.Exec(`INSERT INTO profiles(id,name) VALUES('viewer','Viewer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation) VALUES('viewer',?,111,1,0,0,1),('viewer',?,222,2,1,2,1)`, canonical, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO profile_film_list(profile_id,catalog_id,added_at) VALUES('viewer',?,1),('viewer',?,2)`, canonical, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO profile_ratings(profile_id,catalog_id,rating,provenance,source_id,updated_at) VALUES('viewer',?,3,'local','',1),('viewer',?,5,'local','',2)`, canonical, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO viewing_events(event_id,profile_id,catalog_id,title,kind,event_type,provenance,source_id,recorded_at) VALUES('canonical-event','viewer',?,'Canonical','film','completed','local','canonical-source',1),('member-event','viewer',?,'Member','film','completed','local','member-source',2)`, canonical, member); err != nil {
		t.Fatal(err)
	}
	group, err := c.CreateMediaVersionGroup(context.Background(), "film", canonical, []string{member})
	if err != nil || len(group.Members) != 2 {
		t.Fatalf("group = %#v, %v", group, err)
	}
	visible, _, err := c.Browse("Film", 0, 0)
	if err != nil || len(visible) != 1 || visible[0].ID != canonical {
		t.Fatalf("visible grouped films = %#v, %v", visible, err)
	}
	assertPositions := func() {
		t.Helper()
		for id, want := range map[string]int64{canonical: 111, member: 222} {
			var got int64
			if err := db.QueryRow(`SELECT position_ms FROM progress WHERE profile_id='viewer' AND catalog_id=?`, id).Scan(&got); err != nil || got != want {
				t.Fatalf("history %s = %d, %v; want %d", id, got, err, want)
			}
		}
		for query, want := range map[string]int{
			`SELECT COUNT(*) FROM profile_film_list WHERE profile_id='viewer'`:  2,
			`SELECT SUM(rating) FROM profile_ratings WHERE profile_id='viewer'`: 8,
			`SELECT COUNT(*) FROM viewing_events WHERE profile_id='viewer'`:     2,
		} {
			var got int
			if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
				t.Fatalf("history query %q = %d, %v; want %d", query, got, err, want)
			}
		}
	}
	assertPositions()
	reopened, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := reopened.MediaVersionGroups()
	if err != nil {
		t.Fatalf("reopened groups = %#v, %v", groups, err)
	}
	foundFilmGroup := false
	for _, group := range groups.Groups {
		if group.ID == canonical && len(group.Members) == 2 {
			foundFilmGroup = true
		}
	}
	if !foundFilmGroup {
		t.Fatalf("reopened groups = %#v", groups)
	}
	versions, err := reopened.PlaybackVersions(context.Background(), "viewer", canonical, access.Policy{})
	if err != nil || len(versions) != 2 || versions[0].Item.Width == versions[1].Item.Width {
		t.Fatalf("durable physical properties = %#v, %v", versions, err)
	}
	if _, err := reopened.UngroupMediaVersion(context.Background(), "film", canonical, member); err != nil {
		t.Fatal(err)
	}
	visible, _, err = reopened.Browse("Film", 0, 0)
	if err != nil || len(visible) != 2 {
		t.Fatalf("visible ungrouped films = %#v, %v", visible, err)
	}
	assertPositions()
}

func TestMediaVersionGroupingRejectsEditionConflictAndIncompleteSeries(t *testing.T) {
	c, db, _, _ := versionCatalogFixture(t)
	defer db.Close()
	films, _, _ := c.Browse("Film", 0, 0)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CreateMediaVersionGroup(cancelled, "film", films[0].ID, []string{films[1].ID}); err == nil {
		t.Fatal("cancelled group succeeded")
	}
	var memberships int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_film_version_memberships`).Scan(&memberships); err != nil || memberships != 0 {
		t.Fatalf("cancelled film memberships = %d, %v", memberships, err)
	}
	if _, err := c.SetEditionLabel(context.Background(), "film", films[0].ID, "Theatrical"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetEditionLabel(context.Background(), "film", films[1].ID, "Extended"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateMediaVersionGroup(context.Background(), "film", films[0].ID, []string{films[1].ID}); err != ErrMediaVersionConflict {
		t.Fatalf("edition conflict = %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_film_version_memberships`).Scan(&memberships); err != nil || memberships != 0 {
		t.Fatalf("film memberships = %d, %v", memberships, err)
	}
	series, _, _ := c.Browse("Show", 0, 0)
	if len(series) != 3 {
		t.Fatalf("series = %#v", series)
	}
	if _, err := c.CreateMediaVersionGroup(context.Background(), "series", series[0].ID, []string{series[1].ID}); err != ErrMediaVersionConflict {
		t.Fatalf("incomplete series = %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_series_version_memberships`).Scan(&memberships); err != nil || memberships != 0 {
		t.Fatalf("series memberships = %d, %v", memberships, err)
	}
	group, err := c.CreateMediaVersionGroup(context.Background(), "series", series[0].ID, []string{series[2].ID})
	if err != nil || len(group.Members) != 2 {
		t.Fatalf("complete series group = %#v, %v", group, err)
	}
	canonicalSeries, ok := c.Series(series[0].ID)
	if !ok || len(canonicalSeries.Seasons) != 1 || len(canonicalSeries.Seasons[0].Episodes) != 2 {
		t.Fatalf("canonical series = %#v", canonicalSeries)
	}
	versions, err := c.PlaybackVersions(context.Background(), "", canonicalSeries.Seasons[0].Episodes[0].ID, access.Policy{})
	if err != nil || len(versions) != 2 || versions[0].Version.ID == versions[1].Version.ID {
		t.Fatalf("episode version mapping = %#v, %v", versions, err)
	}
}
