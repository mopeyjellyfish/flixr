package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

type orderProvider struct {
	group catalog.ProviderEpisodeOrderGroup
}

func (orderProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{}, nil
}
func (p orderProvider) EpisodeOrderGroups(context.Context, string, string) ([]catalog.EpisodeOrderGroup, error) {
	return []catalog.EpisodeOrderGroup{{ID: p.group.ID, Name: p.group.Name, Order: p.group.Order}}, nil
}
func (p orderProvider) EpisodeOrderGroup(context.Context, string, string) (catalog.ProviderEpisodeOrderGroup, error) {
	return p.group, nil
}

func TestEpisodeSourceSpansAreExplicit(t *testing.T) {
	tv := t.TempDir()
	for _, name := range []string{
		"Signal/Season 01/Signal S01E100.mkv",
		"Signal/Season 01/Signal S01E1000.mkv",
		"Signal/Season 00/Signal S00E01.mkv",
		"Signal/Season 01/Signal S01E01E02.mkv",
		"Signal/Season 01/Signal S01E03-E05.mkv",
	} {
		file := filepath.Join(tv, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	spans := map[[2]int]int{}
	for _, item := range items {
		spans[[2]int{item.Season, item.Episode}] = item.EpisodeEnd
	}
	for source, want := range map[[2]int]int{{1, 100}: 100, {1, 1000}: 1000, {0, 1}: 1, {1, 1}: 2, {1, 3}: 5} {
		if got := spans[source]; got != want {
			t.Errorf("span %v end=%d, want %d", source, got, want)
		}
	}
}

func TestSavedEpisodeOrderAdvancesPastMappedSpan(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{
		"Signal/Season 01/Signal S01E01-E02.mkv",
		"Signal/Season 01/Signal S01E03.mkv",
	})
	insertProfile(t, db, "ada")
	third, found := c.Item(ids["Signal/Season 01/Signal S01E03.mkv"])
	if !found {
		t.Fatal("episode missing")
	}
	series, ok := c.Series(third.SeriesID)
	if !ok {
		t.Fatal("series missing")
	}
	entries := []catalog.EpisodeOrderEntry{
		{CatalogID: ids["Signal/Season 01/Signal S01E01-E02.mkv"], Mapping: &catalog.EpisodeOrderPosition{Position: 1, EndPosition: 2, Season: 1, Episode: 1, EpisodeEnd: 2}},
		{CatalogID: ids["Signal/Season 01/Signal S01E03.mkv"], Mapping: &catalog.EpisodeOrderPosition{Position: 3, EndPosition: 3, Season: 1, Episode: 3, EpisodeEnd: 3}},
	}
	if _, err := c.SaveEpisodeOrder(context.Background(), series.ID, "dvd", 0, entries); err != nil {
		t.Fatal(err)
	}
	next, err := c.EpisodeAfter("ada", ids["Signal/Season 01/Signal S01E01-E02.mkv"], false)
	if err != nil || next.State != catalog.EpisodeSequenceNext || next.Episode == nil || next.Episode.ID != ids["Signal/Season 01/Signal S01E03.mkv"] {
		t.Fatalf("next = %#v, %v", next, err)
	}
	if _, err := c.SaveEpisodeOrder(context.Background(), series.ID, "aired", 1, nil); err != nil {
		t.Fatal(err)
	}
	next, err = c.EpisodeAfter("ada", ids["Signal/Season 01/Signal S01E01-E02.mkv"], false)
	if err != nil || next.State != catalog.EpisodeSequenceNext || next.Episode == nil || next.Episode.ID != ids["Signal/Season 01/Signal S01E03.mkv"] {
		t.Fatalf("reset next = %#v, %v", next, err)
	}
	bad := entries[0]
	bad.Mapping = &catalog.EpisodeOrderPosition{Position: 1, EndPosition: 1, Season: 1, Episode: 1, EpisodeEnd: 1}
	if _, err := c.SaveEpisodeOrder(context.Background(), series.ID, "dvd", 2, []catalog.EpisodeOrderEntry{bad, entries[1]}); err == nil {
		t.Fatal("short mapping span accepted")
	}
}

func TestEpisodeSpanSurvivesReopen(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	file := filepath.Join(tv, "Signal", "Season 01", "Signal S01E100-E102.mkv")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("span"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v %v", items, err)
	}
	id := items[0].ID
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err = catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	item, ok := c.Item(id)
	if !ok || item.Season != 1 || item.Episode != 100 || item.EpisodeEnd != 102 {
		t.Fatalf("reopened span=%#v", item)
	}
}

func TestSeriesIdentityMergeRejectsSelectedEpisodeOrder(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	for _, name := range []string{"One/Season 01/One S01E01.mkv", "Two/Season 01/Two S01E01.mkv"} {
		file := filepath.Join(tv, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	series, _ := c.EpisodeOrderSeries("", 0, 10)
	if len(series) != 2 {
		t.Fatalf("series=%#v", series)
	}
	detail, err := c.EpisodeOrder(series[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	entry := detail.Entries[0]
	entry.Mapping = &catalog.EpisodeOrderPosition{Position: 1, EndPosition: 1, Season: 1, Episode: 1, EpisodeEnd: 1}
	if _, err := c.SaveEpisodeOrder(context.Background(), detail.SeriesID, "dvd", detail.Revision, []catalog.EpisodeOrderEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MergeIdentity("series", series[0].ID, series[1].ID); err == nil {
		t.Fatal("merge discarded selected order")
	}
}

func TestPreviewEpisodeOrderMapsWholeSourceSpan(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{"Signal/Season 01/Signal S01E01-E02.mkv", "Signal/Season 01/Signal S01E03.mkv"})
	third, _ := c.Item(ids["Signal/Season 01/Signal S01E03.mkv"])
	if _, err := db.Exec(`UPDATE catalog_series SET provider_id='7' WHERE id=?`, third.SeriesID); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	reopened.SetApplicationTMDBToken("token")
	reopened.SetProvider(orderProvider{group: catalog.ProviderEpisodeOrderGroup{EpisodeOrderGroup: catalog.EpisodeOrderGroup{ID: "dvd", Name: "DVD", Order: "dvd"}, Episodes: []catalog.ProviderEpisodeOrderEntry{
		{SourceSeason: 1, SourceEpisode: 1, Mapping: catalog.EpisodeOrderPosition{Position: 1, EndPosition: 1, Season: 1, Episode: 1, EpisodeEnd: 1}},
		{SourceSeason: 1, SourceEpisode: 2, Mapping: catalog.EpisodeOrderPosition{Position: 2, EndPosition: 2, Season: 1, Episode: 2, EpisodeEnd: 2}},
		{SourceSeason: 1, SourceEpisode: 3, Mapping: catalog.EpisodeOrderPosition{Position: 3, EndPosition: 3, Season: 1, Episode: 3, EpisodeEnd: 3}},
	}}})
	detail, err := reopened.PreviewEpisodeOrder(context.Background(), third.SeriesID, "dvd")
	if err != nil || detail.NeedsRepair {
		t.Fatalf("preview=%#v %v", detail, err)
	}
	for _, entry := range detail.Entries {
		if entry.CatalogID == ids["Signal/Season 01/Signal S01E01-E02.mkv"] && (entry.Mapping == nil || entry.Mapping.Position != 1 || entry.Mapping.EndPosition != 2) {
			t.Fatalf("span mapping=%#v", entry.Mapping)
		}
	}
	reopened.SetProvider(orderProvider{group: catalog.ProviderEpisodeOrderGroup{EpisodeOrderGroup: catalog.EpisodeOrderGroup{ID: "dvd", Name: "DVD", Order: "dvd"}, Episodes: []catalog.ProviderEpisodeOrderEntry{
		{SourceSeason: 1, SourceEpisode: 1, Mapping: catalog.EpisodeOrderPosition{Position: 1, EndPosition: 1, Season: 1, Episode: 1, EpisodeEnd: 1}},
		{SourceSeason: 1, SourceEpisode: 1, Mapping: catalog.EpisodeOrderPosition{Position: 2, EndPosition: 2, Season: 1, Episode: 2, EpisodeEnd: 2}},
	}}})
	duplicate, err := reopened.PreviewEpisodeOrder(context.Background(), third.SeriesID, "dvd")
	if err != nil || !duplicate.NeedsRepair {
		t.Fatalf("duplicate preview=%#v %v", duplicate, err)
	}
	reopened.SetProvider(orderProvider{group: catalog.ProviderEpisodeOrderGroup{EpisodeOrderGroup: catalog.EpisodeOrderGroup{ID: "absolute", Name: "Absolute", Order: "absolute"}, Episodes: []catalog.ProviderEpisodeOrderEntry{
		{SourceSeason: 1, SourceEpisode: 1, AbsoluteEpisode: 101, Mapping: catalog.EpisodeOrderPosition{Position: 1, EndPosition: 1, Season: 1, Episode: 101, EpisodeEnd: 101}},
		{SourceSeason: 1, SourceEpisode: 2, AbsoluteEpisode: 102, Mapping: catalog.EpisodeOrderPosition{Position: 2, EndPosition: 2, Season: 1, Episode: 102, EpisodeEnd: 102}},
		{SourceSeason: 1, SourceEpisode: 3, AbsoluteEpisode: 103, Mapping: catalog.EpisodeOrderPosition{Position: 3, EndPosition: 3, Season: 1, Episode: 103, EpisodeEnd: 103}},
	}}})
	absolute, err := reopened.PreviewEpisodeOrder(context.Background(), third.SeriesID, "absolute")
	if err != nil || absolute.NeedsRepair {
		t.Fatalf("canonical-to-absolute preview=%#v %v", absolute, err)
	}
	reopened.SetProvider(orderProvider{group: catalog.ProviderEpisodeOrderGroup{EpisodeOrderGroup: catalog.EpisodeOrderGroup{ID: "dvd", Name: "DVD", Order: "dvd"}, Episodes: []catalog.ProviderEpisodeOrderEntry{
		{SourceSeason: 1, SourceEpisode: 1, Mapping: catalog.EpisodeOrderPosition{Position: 1, EndPosition: 1, Season: 1, Episode: 1, EpisodeEnd: 1}},
		{SourceSeason: 1, SourceEpisode: 2, Mapping: catalog.EpisodeOrderPosition{Position: 3, EndPosition: 3, Season: 1, Episode: 3, EpisodeEnd: 3}},
		{SourceSeason: 1, SourceEpisode: 3, Mapping: catalog.EpisodeOrderPosition{Position: 2, EndPosition: 2, Season: 1, Episode: 2, EpisodeEnd: 2}},
	}}})
	permuted, err := reopened.PreviewEpisodeOrder(context.Background(), third.SeriesID, "dvd")
	if err != nil || !permuted.NeedsRepair {
		t.Fatalf("permuted preview=%#v %v", permuted, err)
	}
}

func TestSaveEpisodeOrderCancellationDoesNotChangeRevision(t *testing.T) {
	c, _, ids := scanEpisodeSequences(t, []string{"Signal/Season 01/Signal S01E01.mkv"})
	item, _ := c.Item(ids["Signal/Season 01/Signal S01E01.mkv"])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.SaveEpisodeOrder(ctx, item.SeriesID, "dvd", 0, []catalog.EpisodeOrderEntry{{CatalogID: item.ID, Mapping: &catalog.EpisodeOrderPosition{Position: 1, EndPosition: 1, Season: 1, Episode: 1, EpisodeEnd: 1}}})
	if err == nil {
		t.Fatal("cancelled save succeeded")
	}
	detail, err := c.EpisodeOrder(item.SeriesID)
	if err != nil || detail.Revision != 0 {
		t.Fatalf("detail=%#v %v", detail, err)
	}
}

func TestSelectedOrderWithoutMappingsRequiresRepairButAiredResetDoesNot(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{"Signal/Season 01/Signal S01E01.mkv", "Signal/Season 01/Signal S01E02.mkv"})
	insertProfile(t, db, "ada")
	item, _ := c.Item(ids["Signal/Season 01/Signal S01E01.mkv"])
	detail, err := c.SaveEpisodeOrder(context.Background(), item.SeriesID, "dvd", 0, nil)
	if err != nil || !detail.NeedsRepair {
		t.Fatalf("empty dvd=%#v %v", detail, err)
	}
	sequence, err := c.EpisodeAfter("ada", item.ID, false)
	if err != nil || sequence.State != catalog.EpisodeSequenceContextUnavailable {
		t.Fatalf("empty dvd next=%#v %v", sequence, err)
	}
	reset, err := c.SaveEpisodeOrder(context.Background(), item.SeriesID, "aired", detail.Revision, nil)
	if err != nil || reset.NeedsRepair {
		t.Fatalf("aired reset=%#v %v", reset, err)
	}
	sequence, err = c.EpisodeAfter("ada", item.ID, false)
	if err != nil || sequence.State != catalog.EpisodeSequenceNext {
		t.Fatalf("aired reset next=%#v %v", sequence, err)
	}
}

func TestSeriesAndListCompleteWhileScanCommits(t *testing.T) {
	c, _, ids := scanEpisodeSequences(t, []string{"Signal/Season 01/Signal S01E01.mkv", "Signal/Season 01/Signal S01E02.mkv"})
	item, _ := c.Item(ids["Signal/Season 01/Signal S01E01.mkv"])
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 20; i++ {
			if _, ok := c.Series(item.SeriesID); !ok {
				return
			}
			if _, err := c.List("", 0, 10); err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 4; i++ {
			if err := c.Scan(context.Background(), 1); err != nil {
				return
			}
		}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for range 2 {
		select {
		case <-done:
		case <-deadline.C:
			t.Fatal("series/list and scan deadlocked")
		}
	}
}
