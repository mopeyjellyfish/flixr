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
	"strings"
	"time"
)

const maxProviderBody = 1 << 20
const maxArtworkBody = 5 << 20

// MetadataProvider is owned by Catalog so tests can use a local fake.
type MetadataProvider interface {
	Lookup(context.Context, string, string, string) (Enrichment, error)
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
	Year                       int
	Synopsis, Poster, Backdrop string
}

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
	q := u.Query()
	q.Set("query", title)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Enrichment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return Enrichment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Enrichment{}, fmt.Errorf("tmdb status %d", resp.StatusCode)
	}
	var body struct {
		Results []struct {
			ID           int    `json:"id"`
			Overview     string `json:"overview"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
			PosterPath   string `json:"poster_path"`
			BackdropPath string `json:"backdrop_path"`
		} `json:"results"`
	}
	if err := decodeLimitedJSON(resp.Body, &body); err != nil {
		return Enrichment{}, err
	}
	if len(body.Results) == 0 {
		return Enrichment{}, nil
	}
	x := body.Results[0]
	date := x.ReleaseDate
	if date == "" {
		date = x.FirstAirDate
	}
	var year int
	fmt.Sscanf(date, "%d", &year)
	return Enrichment{ProviderID: fmt.Sprint(x.ID), Year: year, Synopsis: x.Overview, Poster: x.PosterPath, Backdrop: x.BackdropPath}, nil
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
	resp, err := t.client.Do(req)
	if err != nil {
		return Artwork{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Artwork{}, fmt.Errorf("tmdb image status %d", resp.StatusCode)
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
