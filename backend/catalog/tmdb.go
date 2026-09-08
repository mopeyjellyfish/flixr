package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxProviderBody = 1 << 20
const maxArtworkBody = 5 << 20

// MetadataProvider is owned by Catalog so tests can use a local fake.
type MetadataProvider interface {
	Lookup(context.Context, string, string, string) (Enrichment, error)
}

type MetadataValidator interface {
	Validate(context.Context, string) error
}

type EpisodeProvider interface {
	LookupEpisode(context.Context, string, string, int, int) (Enrichment, error)
}

// ArtworkProvider supplies bounded image bytes for an enrichment image path.
type ArtworkProvider interface {
	FetchArtwork(context.Context, string) (Artwork, error)
}

type Artwork struct {
	Bytes       []byte
	ContentType string
}
type Enrichment struct {
	ProviderID                 string
	Title                      string
	Year                       int
	Synopsis, Poster, Backdrop string
}

var ErrInvalidCredential = errors.New("metadata provider credential is invalid")
var ErrProviderRateLimited = errors.New("metadata provider rate limit reached")

type TMDB struct {
	client                 *http.Client
	apiOrigin, imageOrigin *url.URL
}

func NewTMDB(client *http.Client) *TMDB {
	return NewTMDBWithOrigins(client, "https://api.themoviedb.org", "https://image.tmdb.org")
}

// NewTMDBWithOrigins is for fixed trusted test origins; callers must not use request input here.
func NewTMDBWithOrigins(client *http.Client, apiOrigin, imageOrigin string) *TMDB {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	api, _ := url.Parse(apiOrigin)
	image, _ := url.Parse(imageOrigin)
	return &TMDB{client: client, apiOrigin: api, imageOrigin: image}
}

func (t *TMDB) Validate(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if t.apiOrigin == nil {
		return errors.New("tmdb API origin is invalid")
	}
	u := *t.apiOrigin
	u.Path = path.Join(u.Path, "/3/authentication")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return providerHTTPError(resp.StatusCode)
	}
	return nil
}
func (t *TMDB) Lookup(ctx context.Context, token, kind, title string) (Enrichment, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if t.apiOrigin == nil {
		return Enrichment{}, errors.New("tmdb API origin is invalid")
	}
	u := *t.apiOrigin
	if kind == "series" {
		u.Path = path.Join(u.Path, "/3/search/tv")
	} else {
		u.Path = path.Join(u.Path, "/3/search/movie")
	}
	wantedYear := 0
	if match := metadataYearRE.FindStringSubmatch(title); match != nil {
		wantedYear, _ = strconv.Atoi(match[1])
		title = strings.TrimSpace(title[:len(title)-len(match[0])])
	}
	q := u.Query()
	q.Set("query", title)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Enrichment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.do(req)
	if err != nil {
		return Enrichment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Enrichment{}, providerHTTPError(resp.StatusCode)
	}
	var body struct {
		Results []struct {
			ID            int    `json:"id"`
			Title         string `json:"title"`
			Name          string `json:"name"`
			OriginalTitle string `json:"original_title"`
			OriginalName  string `json:"original_name"`
			Overview      string `json:"overview"`
			ReleaseDate   string `json:"release_date"`
			FirstAirDate  string `json:"first_air_date"`
			PosterPath    string `json:"poster_path"`
			BackdropPath  string `json:"backdrop_path"`
		} `json:"results"`
	}
	if err := decodeLimitedJSON(resp.Body, &body); err != nil {
		return Enrichment{}, err
	}
	var match Enrichment
	for _, x := range body.Results {
		if x.ID <= 0 || normalizedTitle(title) == "" {
			continue
		}
		matchesTitle := false
		for _, candidate := range []string{x.Title, x.Name, x.OriginalTitle, x.OriginalName} {
			if normalizedTitle(candidate) == normalizedTitle(title) {
				matchesTitle = true
			}
		}
		date := x.ReleaseDate
		if kind == "series" {
			date = x.FirstAirDate
		}
		year := 0
		if len(date) >= 4 {
			year, _ = strconv.Atoi(date[:4])
		}
		if !matchesTitle || (wantedYear != 0 && year != wantedYear) {
			continue
		}
		// Ambiguous titles stay local instead of silently selecting the wrong remake.
		if match.ProviderID != "" {
			return Enrichment{}, nil
		}
		name := x.Title
		if kind == "series" {
			name = x.Name
		}
		match = Enrichment{ProviderID: fmt.Sprint(x.ID), Title: name, Year: year, Synopsis: x.Overview, Poster: x.PosterPath, Backdrop: x.BackdropPath}
	}
	return match, nil
}

func (t *TMDB) Candidates(ctx context.Context, token, kind, title, language, region string) ([]Candidate, error) {
	body, err := t.search(ctx, token, kind, title, language, region)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(body.Results))
	for _, x := range body.Results {
		if x.ID <= 0 {
			continue
		}
		name, date := x.Title, x.ReleaseDate
		if kind == "series" {
			name, date = x.Name, x.FirstAirDate
		}
		if name == "" {
			name = x.OriginalTitle
			if kind == "series" {
				name = x.OriginalName
			}
		}
		year := 0
		if len(date) >= 4 {
			year, _ = strconv.Atoi(date[:4])
		}
		out = append(out, Candidate{Provider: "tmdb", ID: fmt.Sprint(x.ID), Title: name, Year: year, Language: language, Region: region, Confidence: titleConfidence(title, name)})
	}
	return out, nil
}

func (t *TMDB) ByID(ctx context.Context, token, kind, providerID, language, region string) (Enrichment, error) {
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(providerID) {
		return Enrichment{}, errors.New("invalid provider identifier")
	}
	if t.apiOrigin == nil {
		return Enrichment{}, errors.New("tmdb API origin is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := *t.apiOrigin
	route := "/3/movie/"
	if kind == "series" {
		route = "/3/tv/"
	}
	u.Path = path.Join(u.Path, route, providerID)
	q := u.Query()
	if language != "" {
		q.Set("language", language)
	}
	if region != "" {
		q.Set("region", region)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Enrichment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.do(req)
	if err != nil {
		return Enrichment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusNotFound {
			return Enrichment{}, nil
		}
		return Enrichment{}, providerHTTPError(resp.StatusCode)
	}
	var x struct {
		ID           int    `json:"id"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		Overview     string `json:"overview"`
		ReleaseDate  string `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
		PosterPath   string `json:"poster_path"`
		BackdropPath string `json:"backdrop_path"`
	}
	if err := decodeLimitedJSON(resp.Body, &x); err != nil {
		return Enrichment{}, err
	}
	if x.ID <= 0 {
		return Enrichment{}, errors.New("provider response has no identifier")
	}
	date := x.ReleaseDate
	if kind == "series" {
		date = x.FirstAirDate
	}
	year := 0
	if len(date) >= 4 {
		year, _ = strconv.Atoi(date[:4])
	}
	name := x.Title
	if kind == "series" {
		name = x.Name
	}
	return Enrichment{ProviderID: fmt.Sprint(x.ID), Title: name, Year: year, Synopsis: x.Overview, Poster: x.PosterPath, Backdrop: x.BackdropPath}, nil
}

func (t *TMDB) LookupEpisode(ctx context.Context, token, seriesID string, season, episode int) (Enrichment, error) {
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(seriesID) || season < 0 || episode < 1 {
		return Enrichment{}, errors.New("invalid episode identifier")
	}
	if t.apiOrigin == nil {
		return Enrichment{}, errors.New("tmdb API origin is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := *t.apiOrigin
	u.Path = path.Join(u.Path, "/3/tv", seriesID, "season", strconv.Itoa(season), "episode", strconv.Itoa(episode))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Enrichment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.do(req)
	if err != nil {
		return Enrichment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusNotFound {
			return Enrichment{}, nil
		}
		return Enrichment{}, providerHTTPError(resp.StatusCode)
	}
	var value struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		Overview  string `json:"overview"`
		AirDate   string `json:"air_date"`
		StillPath string `json:"still_path"`
	}
	if err := decodeLimitedJSON(resp.Body, &value); err != nil {
		return Enrichment{}, err
	}
	if value.ID <= 0 {
		return Enrichment{}, errors.New("provider response has no identifier")
	}
	year := 0
	if len(value.AirDate) >= 4 {
		year, _ = strconv.Atoi(value.AirDate[:4])
	}
	return Enrichment{ProviderID: strconv.Itoa(value.ID), Title: value.Name, Year: year, Synopsis: value.Overview, Backdrop: value.StillPath}, nil
}

type tmdbSearch struct {
	Results []struct {
		ID            int    `json:"id"`
		Title         string `json:"title"`
		Name          string `json:"name"`
		OriginalTitle string `json:"original_title"`
		OriginalName  string `json:"original_name"`
		ReleaseDate   string `json:"release_date"`
		FirstAirDate  string `json:"first_air_date"`
	} `json:"results"`
}

func (t *TMDB) search(ctx context.Context, token, kind, title, language, region string) (tmdbSearch, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if t.apiOrigin == nil {
		return tmdbSearch{}, errors.New("tmdb API origin is invalid")
	}
	u := *t.apiOrigin
	route := "/3/search/movie"
	if kind == "series" {
		route = "/3/search/tv"
	}
	u.Path = path.Join(u.Path, route)
	q := u.Query()
	q.Set("query", title)
	if language != "" {
		q.Set("language", language)
	}
	if region != "" {
		q.Set("region", region)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return tmdbSearch{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.do(req)
	if err != nil {
		return tmdbSearch{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return tmdbSearch{}, providerHTTPError(resp.StatusCode)
	}
	var body tmdbSearch
	if err := decodeLimitedJSON(resp.Body, &body); err != nil {
		return tmdbSearch{}, err
	}
	return body, nil
}

func titleConfidence(query, candidate string) float64 {
	if normalizedTitle(query) == normalizedTitle(candidate) {
		return 1
	}
	return 0.5
}

var metadataYearRE = regexp.MustCompile(`(?:\s+|\s*\()((?:19|20)\d{2})\)?$`)

func normalizedTitle(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func decodeLimitedJSON(r io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(r, maxProviderBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxProviderBody {
		return errors.New("provider response too large")
	}
	return json.Unmarshal(data, value)
}
func (t *TMDB) FetchArtwork(ctx context.Context, imagePath string) (Artwork, error) {
	if t.imageOrigin == nil || !validImagePath(imagePath) {
		return Artwork{}, errors.New("invalid provider image path")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := *t.imageOrigin
	u.Path = path.Join(u.Path, "/t/p/w500", imagePath)
	u.RawQuery = ""
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Artwork{}, err
	}
	resp, err := t.do(req)
	if err != nil {
		return Artwork{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Artwork{}, providerHTTPError(resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return Artwork{}, errors.New("provider image content type is not an image")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkBody+1))
	if err != nil {
		return Artwork{}, err
	}
	if len(data) > maxArtworkBody {
		return Artwork{}, errors.New("provider image too large")
	}
	return Artwork{Bytes: bytes.Clone(data), ContentType: contentType}, nil
}

// do gives a rate-limited provider one bounded retry while preserving cancellation.
func (t *TMDB) do(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests || attempt == 1 {
			return resp, err
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		delay := retryAfter(resp.Header.Get("Retry-After"), time.Now())
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

func providerHTTPError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrInvalidCredential
	case http.StatusTooManyRequests:
		return ErrProviderRateLimited
	default:
		return fmt.Errorf("%w: tmdb status %d", ErrProviderUnavailable, status)
	}
}

func retryAfter(value string, now time.Time) time.Duration {
	const maximum = 5 * time.Second
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, maximum)
	}
	if when, err := http.ParseTime(value); err == nil {
		return min(max(when.Sub(now), time.Duration(0)), maximum)
	}
	return time.Second
}
func validImagePath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#\\") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
				return false
			}
		}
	}
	return true
}
