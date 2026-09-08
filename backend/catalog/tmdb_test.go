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
