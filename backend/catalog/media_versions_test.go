package catalog

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	for name, body := range map[string]string{
		"Film 4K.fra.Commentary.m4a": "member-audio",
		"Film 4K.eng.vtt":            "WEBVTT\n\n00:00.000 --> 00:01.000\nMember subtitle\n",
	} {
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
		switch filepath.Ext(info.Name()) {
		case ".m4a":
			return MediaProperties{Audio: []AudioTrack{{Index: 0, Codec: "aac", Language: "und"}}}, nil
		case ".vtt":
			return MediaProperties{Subtitles: []SubtitleTrack{{Index: 0, Codec: "webvtt", Language: "und"}}}, nil
		}
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

func TestSeriesMediaVersionsBatchesLongSeriesWithoutReprobing(t *testing.T) {
	data, tv := t.TempDir(), t.TempDir()
	const seasons = 2
	const episodesPerSeason = 10
	for _, show := range []string{"Long A", "Long B"} {
		for season := 1; season <= seasons; season++ {
			for episode := 1; episode <= episodesPerSeason; episode++ {
				relative := filepath.Join(show, fmt.Sprintf("Season %02d", season), fmt.Sprintf("%s S%02dE%02d.mp4", show, season, episode))
				path := filepath.Join(tv, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(relative), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var probes atomic.Int64
	c, err := OpenWithProber(db, ProberFunc(func(_ context.Context, _ *os.File) (MediaProperties, error) {
		probes.Add(1)
		return MediaProperties{Container: "mp4", VideoCodec: "h264", Width: 1920, Height: 1080}, nil
	}))
	if err != nil || c.SetRoots("", tv) != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("scan long series: %v", err)
	}
	series, _, err := c.Browse("Long", 0, 10)
	if err != nil || len(series) != 2 {
		t.Fatalf("long series = %#v, %v", series, err)
	}
	group, err := c.CreateMediaVersionGroup(t.Context(), "series", series[0].ID, []string{series[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	before := probes.Load()
	started := time.Now()
	versions, err := c.SeriesMediaVersions(t.Context(), "", group.ID, access.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("batched long-series lookup took %s", elapsed)
	}
	if probes.Load() != before {
		t.Fatalf("detail lookup reprobed sources: %d -> %d", before, probes.Load())
	}
	if len(versions) != seasons*episodesPerSeason {
		t.Fatalf("versioned episodes = %d", len(versions))
	}
	for episodeID, choices := range versions {
		if len(choices) != 2 {
			t.Fatalf("episode %s choices = %#v", episodeID, choices)
		}
	}
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
	viewer, err := c.Viewer("viewer", "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range viewer.Sections {
		for _, item := range section.Items {
			if item.ID == member {
				t.Fatalf("dormant member history appeared in %s", section.Name)
			}
		}
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
	var memberSource PlaybackVersion
	for _, version := range versions {
		if version.Version.ID == member {
			memberSource = version
		}
	}
	if len(memberSource.Item.Audio) != 1 || !memberSource.Item.Audio[0].External || len(memberSource.Item.Subtitles) != 1 || !memberSource.Item.Subtitles[0].External {
		t.Fatalf("member sidecars = audio %#v subtitles %#v", memberSource.Item.Audio, memberSource.Item.Subtitles)
	}
	audio, err := reopened.OpenAudioSource(canonical, memberSource.Item.SourceKey(), memberSource.Item.Audio[0].Index, true)
	if err != nil {
		t.Fatal(err)
	}
	audioBody, readErr := io.ReadAll(audio)
	audio.Close()
	if readErr != nil || string(audioBody) != "member-audio" {
		t.Fatalf("member audio = %q, %v", audioBody, readErr)
	}
	subtitle := memberSource.Item.Subtitles[0]
	subtitleFile, err := reopened.OpenSubtitleSource(canonical, memberSource.Item.SourceKey(), subtitle.SourceKey(), subtitle.Index, true)
	if err != nil {
		t.Fatal(err)
	}
	subtitleBody, readErr := io.ReadAll(subtitleFile)
	subtitleFile.Close()
	if readErr != nil || !strings.Contains(string(subtitleBody), "Member subtitle") {
		t.Fatalf("member subtitle = %q, %v", subtitleBody, readErr)
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
