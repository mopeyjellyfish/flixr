package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

// Demo snapshots and artwork are downloaded explicitly by make demo, never at server startup.
type demoTitle struct {
	Title    string        `json:"title"`
	Year     int           `json:"year"`
	Synopsis string        `json:"synopsis"`
	Genres   []string      `json:"genres"`
	Poster   string        `json:"poster"`
	Backdrop string        `json:"backdrop"`
	Episodes []demoEpisode `json:"episodes,omitempty"`
}
type demoEpisode struct {
	Title  string `json:"title"`
	Season int    `json:"season"`
	Number int    `json:"number"`
}
type demoSnapshot struct {
	Source string      `json:"source"`
	Films  []demoTitle `json:"films"`
	Series []demoTitle `json:"series"`
}

func importDemo(db *sqlite.DB) (bool, error) {
	root, err := os.OpenRoot(filepath.Join(db.DataDir(), "demo"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	file, err := root.Open("catalog.json")
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	var snapshot demoSnapshot
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return false, fmt.Errorf("read demo snapshot: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return false, fmt.Errorf("invalid demo snapshot ending")
	}
	if len(snapshot.Films) != 50 || len(snapshot.Series) != 50 {
		return false, fmt.Errorf("demo snapshot requires exactly 50 films and 50 series")
	}
	// Validate the entire snapshot before changing any catalog rows or cached assets.
	for _, titles := range [][]demoTitle{snapshot.Films, snapshot.Series} {
		seen := map[string]bool{}
		for _, title := range titles {
			slug := demoSlug(title.Title)
			if strings.TrimSpace(title.Title) == "" || len(title.Title) > 300 || seen[slug] || title.Year < 1800 || title.Year > 2200 || len(title.Episodes) > 100 {
				return false, fmt.Errorf("invalid or duplicate demo title %q", title.Title)
			}
			seen[slug] = true
			for _, asset := range []string{title.Poster, title.Backdrop} {
				if asset != "" && (filepath.Base(asset) != asset || asset == "." || asset == "..") {
					return false, fmt.Errorf("invalid demo asset name")
				}
			}
			episodes := map[string]bool{}
			for _, episode := range title.Episodes {
				key := fmt.Sprintf("%d/%d", episode.Season, episode.Number)
				if episode.Season < 1 || episode.Number < 1 || episode.Title == "" || episodes[key] {
					return false, fmt.Errorf("invalid demo episode")
				}
				episodes[key] = true
			}
		}
	}
	c, err := Open(db)
	if err != nil {
		return false, err
	}
	defer c.Shutdown(context.Background())
	// Reuse the same local artwork cache and authenticated serving route as real libraries.
	for _, group := range []struct {
		kind   string
		titles []demoTitle
	}{{"film", snapshot.Films}, {"series", snapshot.Series}} {
		for i := range group.titles {
			title := &group.titles[i]
			catalogID := id(group.kind, "demo/2026/"+group.kind+"/"+demoSlug(title.Title))
			for kind, asset := range map[string]*string{"poster": &title.Poster, "backdrop": &title.Backdrop} {
				if *asset == "" {
					continue
				}
				f, err := root.Open("assets/" + *asset)
				if err != nil {
					return false, fmt.Errorf("open demo artwork: %w", err)
				}
				data, readErr := io.ReadAll(io.LimitReader(f, (8<<20)+1))
				f.Close()
				if readErr != nil || len(data) > 8<<20 || !allowedArtworkContentType(http.DetectContentType(data)) {
					return false, fmt.Errorf("invalid demo artwork %s", *asset)
				}
				*asset, err = c.cacheArtwork(catalogID, kind, Artwork{Bytes: data, ContentType: http.DetectContentType(data)})
				if err != nil {
					return false, err
				}
			}
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var itemIDs, seriesIDs []any
	added := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC).Unix()
	insertItem := func(catalogID, kind, fingerprint, path, seriesID, seasonID string, title demoTitle, rank int) error {
		genres, _ := json.Marshal(title.Genres)
		_, err := tx.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,fingerprint,series_id,season_id,year,synopsis,genres_json,poster,backdrop,added_at,updated_at,playable,demo) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,1) ON CONFLICT(id) DO UPDATE SET title=excluded.title,year=excluded.year,synopsis=excluded.synopsis,genres_json=excluded.genres_json,poster=excluded.poster,backdrop=excluded.backdrop,added_at=excluded.added_at,playable=0 WHERE catalog_items.demo=1`, catalogID, kind, title.Title, path, kind, fingerprint, seriesID, seasonID, title.Year, title.Synopsis, string(genres), title.Poster, title.Backdrop, added-int64(rank), added)
		itemIDs = append(itemIDs, catalogID)
		return err
	}
	for rank, title := range snapshot.Films {
		fingerprint := "demo/2026/film/" + demoSlug(title.Title)
		if err := insertItem(id("film", fingerprint), "film", fingerprint, fingerprint+".demo", "", "", title, rank); err != nil {
			return false, err
		}
	}
	for rank, title := range snapshot.Series {
		fingerprint := "demo/2026/series/" + demoSlug(title.Title)
		seriesID := id("series", fingerprint)
		seriesIDs = append(seriesIDs, seriesID)
		genres, _ := json.Marshal(title.Genres)
		_, err := tx.Exec(`INSERT INTO catalog_series(id,title,year,synopsis,genres_json,poster,backdrop,added_at,updated_at,playable,demo) VALUES(?,?,?,?,?,?,?,?,?,0,1) ON CONFLICT(id) DO UPDATE SET title=excluded.title,year=excluded.year,synopsis=excluded.synopsis,genres_json=excluded.genres_json,poster=excluded.poster,backdrop=excluded.backdrop,added_at=excluded.added_at,playable=0 WHERE catalog_series.demo=1`, seriesID, title.Title, title.Year, title.Synopsis, string(genres), title.Poster, title.Backdrop, added-int64(rank), added)
		if err != nil {
			return false, err
		}
		for _, episode := range title.Episodes {
			seasonID := id("season", fmt.Sprintf("%s/%d", seriesID, episode.Season))
			if _, err := tx.Exec(`INSERT INTO catalog_seasons(id,series_id,number) VALUES(?,?,?) ON CONFLICT(series_id,number) DO NOTHING`, seasonID, seriesID, episode.Season); err != nil {
				return false, err
			}
			epFingerprint := fmt.Sprintf("%s/s%02de%02d", fingerprint, episode.Season, episode.Number)
			ep := demoTitle{Title: episode.Title, Year: title.Year, Genres: title.Genres}
			if err := insertItem(id("episode", epFingerprint), "episode", epFingerprint, fmt.Sprintf("%s/S%02dE%02d.demo", fingerprint, episode.Season, episode.Number), seriesID, seasonID, ep, rank); err != nil {
				return false, err
			}
		}
	}
	// Replace only obsolete synthetic titles; retain credentials, real media, and lists for retained IDs.
	for table, ids := range map[string][]any{"catalog_items": itemIDs, "catalog_series": seriesIDs} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE demo=1 AND id NOT IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+")", ids...); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES('demo_source',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, snapshot.Source); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
