package catalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func likeLiteral(query string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(strings.TrimSpace(query))
}

// Browse projects playable films and non-playable series into the home/search catalog.
func (c *Catalog) Browse(query string, offset, limit int) ([]Item, int, error) {
	if c.db == nil {
		return c.browseMemory(query, offset, limit), c.browseTotal(query), nil
	}
	if offset < 0 {
		return []Item{}, 0, nil
	}
	needle := likeLiteral(query)
	var total int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM (
		SELECT id FROM catalog_items WHERE series_id='' AND title LIKE '%' || ? || '%' ESCAPE '\' COLLATE NOCASE
		UNION ALL
		SELECT id FROM catalog_series WHERE title LIKE '%' || ? || '%' ESCAPE '\' COLLATE NOCASE
	)`, needle, needle).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count browse catalog: %w", err)
	}
	if limit <= 0 {
		limit = total
	}
	rows, err := c.db.Query(`SELECT id,'film' AS kind,title,local_only,provider_id,metadata_provider,metadata_language,metadata_region,match_confidence,owner_matched,year,synopsis,poster,backdrop,genres_json,added_at,playable,demo FROM catalog_items WHERE series_id='' AND title LIKE '%' || ? || '%' ESCAPE '\' COLLATE NOCASE
		UNION ALL
		SELECT id,'series' AS kind,title,local_only,provider_id,metadata_provider,metadata_language,metadata_region,match_confidence,owner_matched,year,synopsis,poster,backdrop,genres_json,added_at,playable,demo FROM catalog_series WHERE title LIKE '%' || ? || '%' ESCAPE '\' COLLATE NOCASE
		ORDER BY title COLLATE NOCASE,id LIMIT ? OFFSET ?`, needle, needle, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("browse catalog: %w", err)
	}
	defer rows.Close()
	out := make([]Item, 0)
	for rows.Next() {
		var x Item
		var local, playable, demo, owner int
		var genres string
		if err := rows.Scan(&x.ID, &x.Kind, &x.Title, &local, &x.ProviderID, &x.Provider, &x.Language, &x.Region, &x.Confidence, &owner, &x.Year, &x.Synopsis, &x.Poster, &x.Backdrop, &genres, &x.AddedAt, &playable, &demo); err != nil {
			return nil, 0, fmt.Errorf("scan browse item: %w", err)
		}
		if err := json.Unmarshal([]byte(genres), &x.Genres); err != nil {
			return nil, 0, fmt.Errorf("decode browse genres: %w", err)
		}
		x.LocalOnly, x.Playable, x.Demo, x.OwnerMatch = local != 0, playable != 0, demo != 0, owner != 0
		out = append(out, x)
	}
	return out, total, rows.Err()
}

func (c *Catalog) browseMemory(query string, offset, limit int) []Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	all := make([]Item, 0, len(c.items)+len(c.series))
	for _, item := range c.items {
		if item.SeriesID == "" {
			all = append(all, item)
		}
	}
	for _, series := range c.series {
		all = append(all, Item{ID: series.ID, Title: series.Title, Kind: "series", LocalOnly: series.LocalOnly, ProviderID: series.ProviderID, Provider: series.Provider, Language: series.Language, Region: series.Region, Confidence: series.Confidence, OwnerMatch: series.OwnerMatch, Year: series.Year, Synopsis: series.Synopsis, Poster: series.Poster, Backdrop: series.Backdrop})
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	filtered := all[:0]
	for _, item := range all {
		if needle == "" || strings.Contains(strings.ToLower(item.Title), needle) {
			filtered = append(filtered, item)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return strings.ToLower(filtered[i].Title) < strings.ToLower(filtered[j].Title) })
	if offset < 0 || offset >= len(filtered) {
		return []Item{}
	}
	if limit <= 0 || offset+limit > len(filtered) {
		limit = len(filtered) - offset
	}
	return filtered[offset : offset+limit]
}
func (c *Catalog) browseTotal(query string) int { return len(c.browseMemory(query, 0, 0)) }

// Series returns ordered seasons and their catalog-ID episode records.
func (c *Catalog) Series(seriesID string) (Series, bool) {
	if c.db == nil {
		return c.seriesMemory(seriesID)
	}
	var out Series
	var local, playable, demo int
	var genres string
	if err := c.db.QueryRow(`SELECT id,title,local_only,provider_id,year,synopsis,poster,backdrop,genres_json,added_at,playable,demo FROM catalog_series WHERE id=?`, seriesID).Scan(&out.ID, &out.Title, &local, &out.ProviderID, &out.Year, &out.Synopsis, &out.Poster, &out.Backdrop, &genres, &out.AddedAt, &playable, &demo); err != nil {
		return Series{}, false
	}
	if json.Unmarshal([]byte(genres), &out.Genres) != nil {
		return Series{}, false
	}
	out.Kind, out.LocalOnly, out.Playable, out.Demo = "series", local != 0, playable != 0, demo != 0
	rows, err := c.db.Query(`SELECT id,kind,title,relative_path,local_only,root_kind,fingerprint,size_bytes,mtime_unix,container,video_codec,video_profile,audio_json,subtitle_json,series_id,provider_id,year,synopsis,poster,backdrop,season_id,genres_json,added_at,playable,demo FROM catalog_items WHERE series_id=? ORDER BY season_id, id`, seriesID)
	if err != nil {
		return Series{}, false
	}
	defer rows.Close()
	bySeason := map[int][]Item{}
	for rows.Next() {
		var x Item
		var local, playable, demo int
		var audio, subtitles, seasonID, genres string
		if err := rows.Scan(&x.ID, &x.Kind, &x.Title, &x.path, &local, &x.rootKind, &x.fingerprint, &x.size, &x.mtime, &x.Container, &x.VideoCodec, &x.VideoProfile, &audio, &subtitles, &x.SeriesID, &x.ProviderID, &x.Year, &x.Synopsis, &x.Poster, &x.Backdrop, &seasonID, &genres, &x.AddedAt, &playable, &demo); err != nil {
			return Series{}, false
		}
		if json.Unmarshal([]byte(audio), &x.Audio) != nil || json.Unmarshal([]byte(subtitles), &x.Subtitles) != nil || json.Unmarshal([]byte(genres), &x.Genres) != nil {
			return Series{}, false
		}
		x.LocalOnly, x.Playable, x.Demo = local != 0, playable != 0, demo != 0
		episodeFields(&x)
		bySeason[x.Season] = append(bySeason[x.Season], x)
	}
	numbers := make([]int, 0, len(bySeason))
	for n := range bySeason {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		episodes := bySeason[n]
		sort.Slice(episodes, func(i, j int) bool { return episodes[i].Episode < episodes[j].Episode })
		out.Seasons = append(out.Seasons, Season{ID: id("season", seriesID+"/"+strconv.Itoa(n)), Number: n, Episodes: episodes})
	}
	return out, true
}

func (c *Catalog) seriesMemory(seriesID string) (Series, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out, ok := c.series[seriesID]
	if !ok {
		return Series{}, false
	}
	seasons := map[int][]Item{}
	for _, item := range c.items {
		if item.SeriesID == seriesID {
			seasons[item.Season] = append(seasons[item.Season], item)
		}
	}
	numbers := make([]int, 0, len(seasons))
	for n := range seasons {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		episodes := seasons[n]
		sort.Slice(episodes, func(i, j int) bool { return episodes[i].Episode < episodes[j].Episode })
		out.Seasons = append(out.Seasons, Season{ID: id("season", seriesID+"/"+strconv.Itoa(n)), Number: n, Episodes: episodes})
	}
	return out, true
}
