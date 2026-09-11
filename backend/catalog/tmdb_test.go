package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTMDBValidatesBearerCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/authentication" || r.Header.Get("Authorization") != "Bearer valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	provider := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	if err := provider.Validate(context.Background(), "valid"); err != nil {
		t.Fatalf("valid credential: %v", err)
	}
	if err := provider.Validate(context.Background(), "invalid"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("invalid credential = %v", err)
	}
}

func TestTMDBClassifiesInvalidAndRateLimitedLookups(t *testing.T) {
	status := http.StatusUnauthorized
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(status)
	}))
	defer server.Close()
	provider := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	if _, err := provider.Lookup(context.Background(), "bad", "film", "Film"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("unauthorized error = %v", err)
	}
	status = http.StatusTooManyRequests
	requests = 0
	if _, err := provider.Lookup(context.Background(), "valid", "film", "Film"); !errors.Is(err, ErrProviderRateLimited) {
		t.Fatalf("rate-limit error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("rate-limit requests = %d, want 2", requests)
	}
}

func TestTMDBClassifiesRateLimitedValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	provider := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	if err := provider.Validate(context.Background(), "valid"); !errors.Is(err, ErrProviderRateLimited) {
		t.Fatalf("validation rate-limit error = %v", err)
	}
}

func TestTMDBLoadsEpisodeDetailsSeparatelyFromSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/tv/7/season/1/episode/2" {
			t.Fatalf("episode path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":22,"name":"The Second Signal","overview":"Episode synopsis","air_date":"2024-02-03","still_path":"/still.jpg"}`))
	}))
	defer server.Close()
	got, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).LookupEpisode(context.Background(), "valid", "7", 1, 2)
	if err != nil || got.ProviderID != "22" || got.Title != "The Second Signal" || got.Year != 2024 || got.Synopsis != "Episode synopsis" || got.Backdrop != "/still.jpg" {
		t.Fatalf("episode = %+v, %v", got, err)
	}
}

func TestTMDBTreatsMissingExactRecordsAsUnmatched(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	provider := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	if got, err := provider.ByID(context.Background(), "valid", "series", "7", "", ""); err != nil || got.ProviderID != "" {
		t.Fatalf("missing series = %+v, %v", got, err)
	}
	if got, err := provider.LookupEpisode(context.Background(), "valid", "7", 1, 2); err != nil || got.ProviderID != "" {
		t.Fatalf("missing episode = %+v, %v", got, err)
	}
}

func TestTMDBRejectsUnsafeAndOversizedArtwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte(strings.Repeat("x", maxArtworkBody+1)))
	}))
	defer server.Close()
	tmdb := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	if _, err := tmdb.FetchArtwork(context.Background(), "https://attacker.example/x.jpg"); err == nil {
		t.Fatal("unsafe provider path was accepted")
	}
	if requests != 0 {
		t.Fatalf("unsafe path made %d requests", requests)
	}
	if _, err := tmdb.FetchArtwork(context.Background(), "/poster.jpg"); err == nil {
		t.Fatal("oversized image was accepted")
	}
}

func TestTMDBRejectsNonImageArtwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("not an image"))
	}))
	defer server.Close()
	if _, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).FetchArtwork(context.Background(), "/poster.jpg"); err == nil {
		t.Fatal("non-image content type was accepted")
	}
}

func TestTMDBSniffsArtworkWhenProviderOmitsContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header()["Content-Type"] = nil
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer server.Close()
	artwork, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).FetchArtwork(context.Background(), "/still.png")
	if err != nil || artwork.ContentType != "image/png" || len(artwork.Bytes) != 8 {
		t.Fatalf("sniffed artwork = %+v, %v", artwork, err)
	}
}

func TestTMDBMatchesTitleAndYearWithoutGuessing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "Dune" {
			t.Errorf("unexpected query %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"id":1,"title":"Dune: Part Two","release_date":"2024-03-01"},{"id":2,"title":"Dune","release_date":"1984-12-14"},{"id":3,"title":"Dune","release_date":"2021-10-22"}]}`))
	}))
	defer server.Close()
	provider := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	for _, test := range []struct{ title, want string }{{"Dune (2021)", "3"}, {"Dune 1984", "2"}, {"Dune", ""}, {"Dune (2000)", ""}} {
		got, err := provider.Lookup(context.Background(), "test", "film", test.title)
		if err != nil || got.ProviderID != test.want {
			t.Errorf("%s: got %+v, %v", test.title, got, err)
		}
	}
}

func TestEpisodeNumbersBeyond99(t *testing.T) {
	for _, name := range []string{"Show.S02E123.mkv", "Show.2x123.mkv"} {
		item := Item{path: "Other S01E01/" + name}
		episodeFields(&item)
		if item.Season != 2 || item.Episode != 123 || title(name) != "Show" {
			t.Errorf("%s: season=%d episode=%d title=%q", name, item.Season, item.Episode, title(name))
		}
	}
}

func TestEpisodeSpanParserRejectsReleaseSuffixesAndPartialChains(t *testing.T) {
	for _, test := range []struct {
		name                 string
		season, episode, end int
	}{
		{"Signal S01E01 1080p.mkv", 1, 1, 1},
		{"Signal S01E01 2026.mkv", 1, 1, 1},
		{"Signal S01E01E02E03.mkv", 0, 0, 0},
		{"Signal S01E01-E02-E03.mkv", 0, 0, 0},
		{"Signal S01E01 E02.mkv", 0, 0, 0},
		{"Signal S01E03-E01.mkv", 0, 0, 0},
	} {
		item := Item{path: test.name}
		episodeFields(&item)
		if item.Season != test.season || item.Episode != test.episode || item.EpisodeEnd != test.end {
			t.Errorf("%s = S%dE%d-%d", test.name, item.Season, item.Episode, item.EpisodeEnd)
		}
	}
}

func TestTMDBTVUsesFirstAirYearAndOriginalName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/search/tv" || r.URL.Query().Get("query") != "Bron Broen" {
			t.Errorf("wrong TV search: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":7,"name":"The Bridge","original_name":"Bron/Broen","first_air_date":"2011-09-21","release_date":"2020-01-01"}]}`))
	}))
	defer server.Close()
	got, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).Lookup(context.Background(), "test", "series", "Bron Broen (2011)")
	if err != nil || got.ProviderID != "7" || got.Year != 2011 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestTMDBRetriesRateLimitOnce(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":7,"title":"Film","release_date":"2024-01-01"}]}`))
	}))
	defer server.Close()
	got, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).Lookup(context.Background(), "test", "film", "Film")
	if err != nil || got.ProviderID != "7" || requests != 2 {
		t.Fatalf("got %+v, requests=%d, err=%v", got, requests, err)
	}
}

func TestTMDBLookupHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).Lookup(ctx, "test", "film", "Film")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled lookup succeeded")
	}
}

func TestTMDBEpisodeOrderGroupsAreBoundedAndCancellable(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/tv/7/episode_groups" {
			close(started)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).EpisodeOrderGroups(ctx, "token", "7")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled episode-group lookup succeeded")
	}
}

func TestTMDBEpisodeOrderGroupTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/tv/7/episode_groups":
			_, _ = w.Write([]byte(`{"results":[{"id":"aired","name":"Aired","type":1},{"id":"absolute","name":"Absolute","type":2},{"id":"dvd","name":"DVD","type":3},{"id":"ignored","type":4}]}`))
		case "/3/tv/episode_group/dvd":
			_, _ = w.Write([]byte(`{"id":"dvd","name":"DVD","type":3,"groups":[{"order":2,"episodes":[{"order":1,"season_number":1,"episode_number":2}]},{"order":1,"episodes":[{"order":0,"season_number":0,"episode_number":1},{"order":1,"season_number":1,"episode_number":1}]}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	provider := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	groups, err := provider.EpisodeOrderGroups(context.Background(), "token", "7")
	if err != nil || len(groups) != 3 || groups[1].Order != "absolute" {
		t.Fatalf("groups=%#v %v", groups, err)
	}
	group, err := provider.EpisodeOrderGroup(context.Background(), "token", "dvd")
	if err != nil || len(group.Episodes) != 3 || !group.Episodes[0].Mapping.Special || group.Episodes[2].Mapping.Position != 3 || group.Episodes[1].Mapping.Season != 2 || group.Order != "dvd" {
		t.Fatalf("group=%#v %v", group, err)
	}
}
