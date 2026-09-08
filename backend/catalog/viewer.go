package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotPlayable       = errors.New("catalog item is not playable")
	ErrCatalogNotFound   = errors.New("catalog item not found")
	ErrInvalidViewerMode = errors.New("invalid viewer mode")
)

type ViewPreference struct {
	View string `json:"view"`
	Sort string `json:"sort"`
}

type ViewerItem struct {
	Item
	Listed                    bool `json:"listed"`
	ContinueWatchingDismissed bool `json:"continue_watching_dismissed"`
}

type ViewerSection struct {
	Name  string       `json:"name"`
	Items []ViewerItem `json:"items"`
}

type ViewerModel struct {
	Preference ViewPreference  `json:"preference"`
	Sections   []ViewerSection `json:"sections,omitempty"`
	Items      []ViewerItem    `json:"items,omitempty"`
}

type viewerItem struct {
	Item
	lastProgress int64
	listAdded    int64
	completed    bool
	dismissed    bool
}

func validMedia(media string) bool { return media == "all" || media == "film" || media == "series" }
func validPreference(p ViewPreference) bool {
	return (p.View == "rows" || p.View == "grid") && (p.Sort == "title" || p.Sort == "year" || p.Sort == "added" || p.Sort == "watched")
}

func (c *Catalog) Preference(profileID, media string) (ViewPreference, error) {
	if c.db == nil || profileID == "" || !validMedia(media) {
		return ViewPreference{}, ErrInvalidViewerMode
	}
	if _, err := c.db.Exec(`INSERT INTO profile_view_preferences(profile_id,media,view_mode,sort_mode) VALUES(?,?, 'rows','title') ON CONFLICT(profile_id,media) DO NOTHING`, profileID, media); err != nil {
		return ViewPreference{}, fmt.Errorf("save default viewer preference: %w", err)
	}
	var p ViewPreference
	if err := c.db.QueryRow(`SELECT view_mode,sort_mode FROM profile_view_preferences WHERE profile_id=? AND media=?`, profileID, media).Scan(&p.View, &p.Sort); err != nil {
		return ViewPreference{}, fmt.Errorf("load viewer preference: %w", err)
	}
	return p, nil
}

func (c *Catalog) SavePreference(profileID, media string, p ViewPreference) (ViewPreference, error) {
	if c.db == nil || profileID == "" || !validMedia(media) || !validPreference(p) {
		return ViewPreference{}, ErrInvalidViewerMode
	}
	if _, err := c.db.Exec(`INSERT INTO profile_view_preferences(profile_id,media,view_mode,sort_mode) VALUES(?,?,?,?) ON CONFLICT(profile_id,media) DO UPDATE SET view_mode=excluded.view_mode,sort_mode=excluded.sort_mode`, profileID, media, p.View, p.Sort); err != nil {
		return ViewPreference{}, fmt.Errorf("save viewer preference: %w", err)
	}
	return p, nil
}

func (c *Catalog) SetListed(profileID, kind, id string, listed bool) error {
	if c.db == nil || profileID == "" || id == "" || (kind != "film" && kind != "series") {
		return ErrCatalogNotFound
	}
	table, exists := "profile_film_list", "SELECT 1 FROM catalog_items WHERE id=? AND kind='film' AND series_id=''"
	if kind == "series" {
		table, exists = "profile_series_list", "SELECT 1 FROM catalog_series WHERE id=?"
	}
	var found int
	if err := c.db.QueryRow(exists, id).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCatalogNotFound
		}
		return fmt.Errorf("check catalog list item: %w", err)
	}
	if listed {
		_, err := c.db.Exec("INSERT INTO "+table+"(profile_id,catalog_id,added_at) VALUES(?,?,?) ON CONFLICT(profile_id,catalog_id) DO NOTHING", profileID, id, time.Now().Unix())
		if err != nil {
			return fmt.Errorf("add catalog list item: %w", err)
		}
		return nil
	}
	if _, err := c.db.Exec("DELETE FROM "+table+" WHERE profile_id=? AND catalog_id=?", profileID, id); err != nil {
		return fmt.Errorf("remove catalog list item: %w", err)
	}
	return nil
}

// SetContinueWatchingDismissed changes only the active profile's presentation
// of a durable logical film or series. Progress, history, and My List remain
// independent records.
func (c *Catalog) SetContinueWatchingDismissed(profileID, kind, id string, dismissed bool) error {
	if c.db == nil || profileID == "" || id == "" || (kind != "film" && kind != "series") {
		return ErrCatalogNotFound
	}
	exists := "SELECT 1 FROM catalog_items WHERE id=? AND kind='film' AND series_id='' AND merged_into=''"
	if kind == "series" {
		exists = "SELECT 1 FROM catalog_series WHERE id=? AND merged_into=''"
	}
	var found int
	if err := c.db.QueryRow(exists, id).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCatalogNotFound
		}
		return fmt.Errorf("check Continue Watching item: %w", err)
	}
	if dismissed {
		if _, err := c.db.Exec(`INSERT INTO profile_continue_watching_dismissals(profile_id,catalog_kind,catalog_id,dismissed_at) VALUES(?,?,?,?) ON CONFLICT(profile_id,catalog_kind,catalog_id) DO UPDATE SET dismissed_at=excluded.dismissed_at`, profileID, kind, id, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("hide Continue Watching item: %w", err)
		}
		return nil
	}
	if _, err := c.db.Exec(`DELETE FROM profile_continue_watching_dismissals WHERE profile_id=? AND catalog_kind=? AND catalog_id=?`, profileID, kind, id); err != nil {
		return fmt.Errorf("restore Continue Watching item: %w", err)
	}
	return nil
}

// AcceptContinueWatching restores a dismissed logical title only when a new
// user-initiated playback plan has been admitted. Recovery and automatic
// episode advancement deliberately leave the dismissal in place.
func (c *Catalog) AcceptContinueWatching(profileID, catalogID string, userInitiated bool) error {
	if !userInitiated {
		return nil
	}
	if c.db == nil || profileID == "" || catalogID == "" {
		return ErrCatalogNotFound
	}
	var kind, seriesID string
	if err := c.db.QueryRow(`SELECT kind,series_id FROM catalog_items WHERE id=? AND merged_into=''`, catalogID).Scan(&kind, &seriesID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCatalogNotFound
		}
		return fmt.Errorf("resolve accepted viewing: %w", err)
	}
	targetKind, targetID := "film", catalogID
	if kind == "episode" && seriesID != "" {
		targetKind, targetID = "series", seriesID
	} else if kind != "film" || seriesID != "" {
		return ErrCatalogNotFound
	}
	if _, err := c.db.Exec(`DELETE FROM profile_continue_watching_dismissals WHERE profile_id=? AND catalog_kind=? AND catalog_id=?`, profileID, targetKind, targetID); err != nil {
		return fmt.Errorf("restore accepted viewing: %w", err)
	}
	return nil
}

// WatchedItems resolves a public action target to the playable records it owns.
func (c *Catalog) WatchedItems(kind, id string, season int) ([]string, error) {
	if c.db == nil || id == "" {
		return nil, ErrCatalogNotFound
	}
	query, args := "", []any{}
	switch kind {
	case "film", "episode":
		query, args = "SELECT id FROM catalog_items WHERE id=? AND kind=?", []any{id, kind}
	case "series":
		query, args = "SELECT id FROM catalog_items WHERE series_id=?", []any{id}
	case "season":
		if season < 1 {
			return nil, ErrCatalogNotFound
		}
		query, args = "SELECT id FROM catalog_items WHERE series_id=? AND season_id=?", []any{id, season}
	default:
		return nil, ErrCatalogNotFound
	}
	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve watched items: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var itemID string
		if err := rows.Scan(&itemID); err != nil {
			return nil, fmt.Errorf("scan watched item: %w", err)
		}
		ids = append(ids, itemID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate watched items: %w", err)
	}
	if len(ids) == 0 {
		return nil, ErrCatalogNotFound
	}
	return ids, nil
}

func (c *Catalog) Viewer(profileID, media string) (ViewerModel, error) {
	preference, err := c.Preference(profileID, media)
	if err != nil {
		return ViewerModel{}, err
	}
	items, err := c.viewerItems(profileID, media)
	if err != nil {
		return ViewerModel{}, err
	}
	model := ViewerModel{Preference: preference}
	if preference.View == "grid" {
		sortViewerItems(items, preference.Sort)
		model.Items = publicItems(items)
		return model, nil
	}
	model.Sections = viewerSections(items)
	return model, nil
}

func (c *Catalog) viewerItems(profileID, media string) ([]viewerItem, error) {
	if c.db == nil {
		return nil, ErrInvalidViewerMode
	}
	rows, err := c.db.Query(`SELECT id,kind,title,local_only,provider_id,year,synopsis,poster,backdrop,genres_json,added_at,playable,demo,last_progress_at,list_added,completed,dismissed FROM (
		SELECT i.id,'film' AS kind,i.title,i.local_only,i.provider_id,i.year,i.synopsis,i.poster,i.backdrop,i.genres_json,i.added_at,i.playable,i.demo,COALESCE(p.updated_at,0) AS last_progress_at,COALESCE(l.added_at,0) AS list_added,COALESCE(p.completed,0) AS completed,COALESCE(d.catalog_id IS NOT NULL,0) AS dismissed
		FROM catalog_items i LEFT JOIN progress p ON p.catalog_id=i.id AND p.profile_id=? LEFT JOIN profile_film_list l ON l.catalog_id=i.id AND l.profile_id=? LEFT JOIN profile_continue_watching_dismissals d ON d.profile_id=? AND d.catalog_kind='film' AND d.catalog_id=i.id WHERE i.series_id='' AND i.merged_into=''
		UNION ALL
		SELECT s.id,'series' AS kind,s.title,s.local_only,s.provider_id,s.year,s.synopsis,s.poster,s.backdrop,s.genres_json,s.added_at,s.playable,s.demo,COALESCE(w.updated_at,0) AS last_progress_at,COALESCE(l.added_at,0) AS list_added,COALESCE(w.completed,0) AS completed,COALESCE(d.catalog_id IS NOT NULL,0) AS dismissed
		FROM catalog_series s LEFT JOIN (SELECT i.series_id,MAX(COALESCE(p.updated_at,0)) AS updated_at,MIN(COALESCE(p.completed,0)) AS completed FROM catalog_items i LEFT JOIN progress p ON p.catalog_id=i.id AND p.profile_id=? WHERE i.series_id<>'' GROUP BY i.series_id) w ON w.series_id=s.id LEFT JOIN profile_series_list l ON l.catalog_id=s.id AND l.profile_id=? LEFT JOIN profile_continue_watching_dismissals d ON d.profile_id=? AND d.catalog_kind='series' AND d.catalog_id=s.id WHERE s.merged_into=''
	) WHERE ?='all' OR kind=?`, profileID, profileID, profileID, profileID, profileID, profileID, media, media)
	if err != nil {
		return nil, fmt.Errorf("query viewer catalog: %w", err)
	}
	defer rows.Close()
	items := []viewerItem{}
	for rows.Next() {
		var item viewerItem
		var local, playable, demo, completed int
		var genres string
		var dismissed int
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &local, &item.ProviderID, &item.Year, &item.Synopsis, &item.Poster, &item.Backdrop, &genres, &item.AddedAt, &playable, &demo, &item.lastProgress, &item.listAdded, &completed, &dismissed); err != nil {
			return nil, fmt.Errorf("scan viewer catalog: %w", err)
		}
		if err := json.Unmarshal([]byte(genres), &item.Genres); err != nil {
			return nil, fmt.Errorf("decode viewer genres: %w", err)
		}
		item.LocalOnly, item.Playable, item.Demo, item.completed, item.dismissed = local != 0, playable != 0, demo != 0, completed != 0, dismissed != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate viewer catalog: %w", err)
	}
	return items, nil
}

func viewerSections(items []viewerItem) []ViewerSection {
	sections := []ViewerSection{
		{Name: "Continue Watching", Items: publicItems(filterViewerItems(items, func(item viewerItem) bool { return item.lastProgress > 0 && !item.completed && !item.dismissed }, "watched"))},
		{Name: "New", Items: publicItems(sortedViewerItems(items, "added"))},
		{Name: "My List", Items: publicItems(filterViewerItems(items, func(item viewerItem) bool { return item.listAdded > 0 }, "list"))},
	}
	genres := map[string]struct{}{}
	for _, item := range items {
		for _, genre := range item.Genres {
			if genre != "" {
				genres[genre] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(genres))
	for genre := range genres {
		names = append(names, genre)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	for _, genre := range names {
		sections = append(sections, ViewerSection{Name: genre, Items: publicItems(filterViewerItems(items, func(item viewerItem) bool {
			for _, itemGenre := range item.Genres {
				if itemGenre == genre {
					return true
				}
			}
			return false
		}, "title"))})
	}
	return sections
}

func filterViewerItems(items []viewerItem, keep func(viewerItem) bool, mode string) []viewerItem {
	filtered := make([]viewerItem, 0)
	for _, item := range items {
		if keep(item) {
			filtered = append(filtered, item)
		}
	}
	sortViewerItems(filtered, mode)
	return filtered
}

func sortedViewerItems(items []viewerItem, mode string) []viewerItem {
	out := append([]viewerItem(nil), items...)
	sortViewerItems(out, mode)
	return out
}

func sortViewerItems(items []viewerItem, mode string) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch mode {
		case "year":
			if a.Year != b.Year {
				return a.Year > b.Year
			}
			if titleCompare(a.Title, b.Title) != 0 {
				return titleCompare(a.Title, b.Title) < 0
			}
		case "added", "list":
			left, right := a.AddedAt, b.AddedAt
			if mode == "list" {
				left, right = a.listAdded, b.listAdded
			}
			if left != right {
				return left > right
			}
		case "watched":
			if a.lastProgress != b.lastProgress {
				return a.lastProgress > b.lastProgress
			}
		default:
			if titleCompare(a.Title, b.Title) != 0 {
				return titleCompare(a.Title, b.Title) < 0
			}
		}
		return a.ID < b.ID
	})
}

func titleCompare(left, right string) int {
	left, right = strings.ToLower(left), strings.ToLower(right)
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func publicItems(items []viewerItem) []ViewerItem {
	out := make([]ViewerItem, len(items))
	for i, item := range items {
		out[i] = ViewerItem{Item: item.Item, Listed: item.listAdded > 0, ContinueWatchingDismissed: item.dismissed}
	}
	return out
}

func (c *Catalog) PlaybackItem(id string) (Item, error) {
	return c.PlaybackItemContext(context.Background(), id)
}

func (c *Catalog) PlaybackItemContext(ctx context.Context, id string) (Item, error) {
	if err := c.hydrateLegacySource(ctx, id); err != nil {
		return Item{}, err
	}
	if err := c.refreshSourceProof(ctx, id); err != nil {
		return Item{}, err
	}
	c.mu.RLock()
	item, ok := c.playbackSource(id)
	c.mu.RUnlock()
	if !ok {
		return Item{}, ErrCatalogNotFound
	}
	if !item.Playable {
		return Item{}, ErrNotPlayable
	}
	c.mu.RLock()
	item.sourceRoot = c.film
	if item.rootKind == "episode" {
		item.sourceRoot = c.tv
	}
	c.mu.RUnlock()
	return item, nil
}
