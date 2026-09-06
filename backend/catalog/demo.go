package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

type demoRecord struct {
	Title  string
	Genres []string
}

// OpenDemo explicitly seeds the provider-free catalog before loading its public maps.
func OpenDemo(db *sqlite.DB) (*Catalog, error) {
	if db == nil {
		return nil, fmt.Errorf("demo catalog requires a database")
	}
	if err := seedDemo(db); err != nil {
		return nil, err
	}
	return Open(db)
}

var demoFilms = []demoRecord{
	{"Project Hail Mary", []string{"Science Fiction", "Adventure"}}, {"The Super Mario Galaxy Movie", []string{"Animation", "Family"}}, {"The Odyssey", []string{"Adventure", "Drama"}}, {"Avengers: Doomsday", []string{"Action", "Science Fiction"}}, {"Spider-Man: Brand New Day", []string{"Action", "Adventure"}}, {"Supergirl", []string{"Action", "Science Fiction"}}, {"Toy Story 5", []string{"Animation", "Family"}}, {"Moana", []string{"Adventure", "Family"}}, {"Shrek 5", []string{"Animation", "Comedy"}}, {"The Mandalorian and Grogu", []string{"Science Fiction", "Adventure"}}, {"Dune: Messiah", []string{"Science Fiction", "Drama"}}, {"The Hunger Games: Sunrise on the Reaping", []string{"Drama", "Adventure"}}, {"The Batman Part II", []string{"Action", "Crime"}}, {"Masters of the Universe", []string{"Fantasy", "Adventure"}}, {"The Bride!", []string{"Horror", "Drama"}}, {"The Dog Stars", []string{"Science Fiction", "Drama"}}, {"The Devil Wears Prada 2", []string{"Comedy", "Drama"}}, {"Jumanji 3", []string{"Adventure", "Comedy"}}, {"Ice Age 6", []string{"Animation", "Family"}}, {"The Legend of Aang: The Last Airbender", []string{"Animation", "Fantasy"}},
}
var demoSeries = []demoRecord{
	{"The Bear", []string{"Drama", "Comedy"}}, {"Euphoria", []string{"Drama"}}, {"The Last of Us", []string{"Drama", "Science Fiction"}}, {"House of the Dragon", []string{"Drama", "Fantasy"}}, {"The Boys", []string{"Action", "Comedy"}}, {"Fallout", []string{"Science Fiction", "Drama"}}, {"Bridgerton", []string{"Drama", "Romance"}}, {"Stranger Things", []string{"Drama", "Science Fiction"}}, {"Andor", []string{"Science Fiction", "Drama"}}, {"One Piece", []string{"Action", "Adventure"}}, {"The White Lotus", []string{"Comedy", "Drama"}}, {"The Diplomat", []string{"Drama", "Thriller"}}, {"Severance", []string{"Science Fiction", "Drama"}}, {"The Night Agent", []string{"Action", "Thriller"}}, {"Wednesday", []string{"Fantasy", "Comedy"}}, {"The Witcher", []string{"Fantasy", "Drama"}}, {"The Gilded Age", []string{"Drama"}}, {"The Morning Show", []string{"Drama"}}, {"Only Murders in the Building", []string{"Comedy", "Mystery"}}, {"Slow Horses", []string{"Drama", "Thriller"}},
}

func seedDemo(db *sqlite.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	added := time.Date(2026, time.August, 30, 0, 0, 0, 0, time.UTC).Unix()
	insertItem := func(x Item) error {
		genres, err := json.Marshal(x.Genres)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,fingerprint,size_bytes,mtime_unix,container,video_codec,video_profile,audio_json,subtitle_json,series_id,season_id,provider_id,year,synopsis,poster,backdrop,updated_at,genres_json,added_at,playable,demo) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, x.ID, x.Kind, x.Title, x.path, 1, x.rootKind, x.fingerprint, 0, 0, "", "", "", "[]", "[]", x.SeriesID, "", "", 2026, "", "", "", added, string(genres), added, 0, 1)
		return err
	}
	for _, record := range demoFilms {
		slug := demoSlug(record.Title)
		x := Item{ID: id("film", "demo/2026/film/"+slug), Kind: "film", Title: record.Title, Genres: record.Genres, path: record.Title + ".2026.demo", rootKind: "film", fingerprint: "demo/2026/film/" + slug}
		if err := insertItem(x); err != nil {
			return fmt.Errorf("seed demo film %q: %w", record.Title, err)
		}
	}
	for _, record := range demoSeries {
		slug := demoSlug(record.Title)
		seriesID := id("series", "demo/2026/series/"+slug)
		genres, _ := json.Marshal(record.Genres)
		if _, err := tx.Exec(`INSERT INTO catalog_series(id,title,local_only,provider_id,year,synopsis,poster,backdrop,updated_at,genres_json,added_at,playable,demo) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, seriesID, record.Title, 1, "", 2026, "", "", "", added, string(genres), added, 0, 1); err != nil {
			return fmt.Errorf("seed demo series %q: %w", record.Title, err)
		}
		seasonID := id("season", seriesID+"/1")
		if _, err := tx.Exec(`INSERT INTO catalog_seasons(id,series_id,number) VALUES(?,?,?) ON CONFLICT(series_id,number) DO NOTHING`, seasonID, seriesID, 1); err != nil {
			return err
		}
		for episode := range 2 {
			number := episode + 1
			fingerprint := fmt.Sprintf("demo/2026/series/%s/s01e%02d", slug, number)
			x := Item{ID: id("episode", fingerprint), Kind: "episode", Title: fmt.Sprintf("%s Episode %d", record.Title, number), Genres: record.Genres, path: fmt.Sprintf("%s/%s.S01E%02d.demo", record.Title, record.Title, number), rootKind: "episode", fingerprint: fingerprint, SeriesID: seriesID}
			if err := insertItem(x); err != nil {
				return fmt.Errorf("seed demo episode %q: %w", record.Title, err)
			}
			if _, err := tx.Exec("UPDATE catalog_items SET season_id=? WHERE id=?", seasonID, x.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func demoSlug(title string) string {
	return strings.NewReplacer(" ", "-", ":", "", "!", "", ",", "", "'", "").Replace(strings.ToLower(title))
}
