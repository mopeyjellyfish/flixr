package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/access"
)

var (
	ErrNotPlayable        = errors.New("catalog item is not playable")
	ErrCatalogNotFound    = errors.New("catalog item not found")
	ErrInvalidViewerMode  = errors.New("invalid viewer mode")
	ErrInvalidViewerPage  = errors.New("invalid viewer page")
	defaultViewerPageSize = 48
	maxViewerPageSize     = 100
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
	Name       string       `json:"name"`
	Items      []ViewerItem `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type ViewerModel struct {
	Preference ViewPreference  `json:"preference"`
	Sections   []ViewerSection `json:"sections,omitempty"`
	Items      []ViewerItem    `json:"items,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
type ViewerProfileState struct {
	Listed                    bool `json:"listed"`
	ContinueWatchingDismissed bool `json:"continue_watching_dismissed"`
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
	return c.preferenceContext(context.Background(), profileID, media)
}

func (c *Catalog) preferenceContext(ctx context.Context, profileID, media string) (ViewPreference, error) {
	if c.db == nil || profileID == "" || !validMedia(media) {
		return ViewPreference{}, ErrInvalidViewerMode
	}
	if _, err := c.db.Writer().ExecContext(ctx, `INSERT INTO profile_view_preferences(profile_id,media,view_mode,sort_mode) VALUES(?,?, 'rows','title') ON CONFLICT(profile_id,media) DO NOTHING`, profileID, media); err != nil {
		return ViewPreference{}, fmt.Errorf("save default viewer preference: %w", err)
	}
	var p ViewPreference
	if err := c.db.Reader().QueryRowContext(ctx, `SELECT view_mode,sort_mode FROM profile_view_preferences WHERE profile_id=? AND media=?`, profileID, media).Scan(&p.View, &p.Sort); err != nil {
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
	return c.ViewerPage(context.Background(), profileID, media, "", "", defaultViewerPageSize, access.Unrestricted())
}

type viewerCursor struct {
	Version     int    `json:"v"`
	ProfileHash string `json:"profile"`
	PolicyHash  string `json:"policy"`
	Media       string `json:"media"`
	Section     string `json:"section"`
	View        string `json:"view"`
	Sort        string `json:"sort"`
	Text        string `json:"text"`
	Number      int64  `json:"number"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Start       bool   `json:"start,omitempty"`
}

func (c *Catalog) ViewerPage(ctx context.Context, profileID, media, section, encodedCursor string, limit int, policy access.Policy) (ViewerModel, error) {
	if profileID == "" || !validMedia(media) || limit < 1 || limit > maxViewerPageSize {
		return ViewerModel{}, ErrInvalidViewerPage
	}
	preference, err := c.preferenceContext(ctx, profileID, media)
	if err != nil {
		return ViewerModel{}, err
	}
	binding := viewerCursor{Version: 1, ProfileHash: viewerBinding(profileID), PolicyHash: policyBinding(policy), Media: media, Section: section, View: preference.View, Sort: preference.Sort}
	cursor, err := decodeViewerCursor(encodedCursor, binding)
	if err != nil {
		return ViewerModel{}, err
	}
	model := ViewerModel{Preference: preference}
	if preference.View == "grid" {
		items, next, err := c.viewerPageItems(ctx, profileID, media, "", preference.Sort, cursor, binding, limit, policy)
		if err != nil {
			return ViewerModel{}, err
		}
		model.Items, model.NextCursor = items, next
		return model, nil
	}
	if section != "" {
		items, next, err := c.viewerPageItems(ctx, profileID, media, section, sectionSort(section), cursor, binding, limit, policy)
		if err != nil {
			return ViewerModel{}, err
		}
		model.Sections = []ViewerSection{{Name: section, Items: items, NextCursor: next}}
		model.NextCursor = next
		return model, nil
	}
	for _, name := range []string{"Continue Watching", "New", "My List"} {
		sectionBinding := binding
		sectionBinding.Section = name
		items, next, err := c.viewerPageItems(ctx, profileID, media, name, sectionSort(name), viewerCursor{}, sectionBinding, limit, policy)
		if err != nil {
			return ViewerModel{}, err
		}
		model.Sections = append(model.Sections, ViewerSection{Name: name, Items: items, NextCursor: next})
	}
	genres, err := c.viewerGenres(ctx, media, policy)
	if err != nil {
		return ViewerModel{}, err
	}
	for _, name := range genres {
		sectionBinding := binding
		sectionBinding.Section = name
		sectionBinding.Start = true
		model.Sections = append(model.Sections, ViewerSection{Name: name, Items: []ViewerItem{}, NextCursor: encodeViewerCursor(sectionBinding)})
	}
	return model, nil
}

func viewerBinding(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }
func policyBinding(policy access.Policy) string {
	data, _ := json.Marshal(policy)
	return viewerBinding(string(data))
}

func decodeViewerCursor(encoded string, binding viewerCursor) (viewerCursor, error) {
	if encoded == "" {
		return viewerCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return viewerCursor{}, ErrInvalidViewerPage
	}
	var cursor viewerCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != binding.Version || cursor.ProfileHash != binding.ProfileHash || cursor.PolicyHash != binding.PolicyHash || cursor.Media != binding.Media || cursor.Section != binding.Section || cursor.View != binding.View || cursor.Sort != binding.Sort || (!cursor.Start && (cursor.ID == "" || (cursor.Kind != "film" && cursor.Kind != "series"))) {
		return viewerCursor{}, ErrInvalidViewerPage
	}
	return cursor, nil
}

func encodeViewerCursor(cursor viewerCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}
func (c *Catalog) ViewerItemState(ctx context.Context, profileID, kind, id string, policy access.Policy) (ViewerProfileState, error) {
	if c.db == nil || profileID == "" || id == "" || (kind != "film" && kind != "series") {
		return ViewerProfileState{}, ErrCatalogNotFound
	}
	content, found, err := c.accessContentContext(ctx, kind, id)
	if err != nil {
		return ViewerProfileState{}, err
	}
	if !found {
		return ViewerProfileState{}, ErrCatalogNotFound
	}
	if !policy.Allows(content) {
		return ViewerProfileState{}, ErrAccessDenied
	}
	listTable := "profile_film_list"
	if kind == "series" {
		listTable = "profile_series_list"
	}
	var listed, dismissed int
	err = c.db.Reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+listTable+` WHERE profile_id=? AND catalog_id=?),EXISTS(SELECT 1 FROM profile_continue_watching_dismissals WHERE profile_id=? AND catalog_kind=? AND catalog_id=?)`, profileID, id, profileID, kind, id).Scan(&listed, &dismissed)
	if err != nil {
		return ViewerProfileState{}, fmt.Errorf("load viewer item state: %w", err)
	}
	return ViewerProfileState{Listed: listed != 0, ContinueWatchingDismissed: dismissed != 0}, nil
}

func sectionSort(section string) string {
	switch section {
	case "Continue Watching":
		return "watched"
	case "New":
		return "added"
	case "My List":
		return "list"
	default:
		return "title"
	}
}

func (c *Catalog) viewerPageItems(ctx context.Context, profileID, media, section, mode string, cursor, binding viewerCursor, limit int, policy access.Policy) ([]ViewerItem, string, error) {
	if c.db == nil {
		return nil, "", ErrInvalidViewerMode
	}
	query := `SELECT id,kind,title,local_only,provider_id,year,synopsis,poster,backdrop,genres_json,added_at,playable,demo,last_progress_at,list_added,completed,dismissed FROM (
		SELECT i.id,'film' AS kind,i.title,i.local_only,i.provider_id,i.year,i.synopsis,i.poster,i.backdrop,i.genres_json,i.added_at,i.playable,i.demo,COALESCE(p.updated_at,0) AS last_progress_at,COALESCE(l.added_at,0) AS list_added,COALESCE(p.completed,0) AS completed,COALESCE(d.catalog_id IS NOT NULL,0) AS dismissed
		FROM catalog_items i LEFT JOIN progress p ON p.catalog_id=i.id AND p.profile_id=? LEFT JOIN profile_film_list l ON l.catalog_id=i.id AND l.profile_id=? LEFT JOIN profile_continue_watching_dismissals d ON d.profile_id=? AND d.catalog_kind='film' AND d.catalog_id=i.id WHERE i.series_id='' AND i.merged_into='' AND NOT EXISTS (SELECT 1 FROM catalog_film_version_memberships v WHERE v.member_catalog_id=i.id)
		UNION ALL
		SELECT s.id,'series' AS kind,s.title,s.local_only,s.provider_id,s.year,s.synopsis,s.poster,s.backdrop,s.genres_json,s.added_at,s.playable,s.demo,COALESCE(w.updated_at,0) AS last_progress_at,COALESCE(l.added_at,0) AS list_added,COALESCE(w.completed,0) AS completed,COALESCE(d.catalog_id IS NOT NULL,0) AS dismissed
		FROM catalog_series s LEFT JOIN (SELECT i.series_id,MAX(COALESCE(p.updated_at,0)) AS updated_at,MIN(COALESCE(p.completed,0)) AS completed FROM catalog_items i LEFT JOIN progress p ON p.catalog_id=i.id AND p.profile_id=? WHERE i.series_id<>'' GROUP BY i.series_id) w ON w.series_id=s.id LEFT JOIN profile_series_list l ON l.catalog_id=s.id AND l.profile_id=? LEFT JOIN profile_continue_watching_dismissals d ON d.profile_id=? AND d.catalog_kind='series' AND d.catalog_id=s.id WHERE s.merged_into='' AND NOT EXISTS (SELECT 1 FROM catalog_series_version_memberships v WHERE v.member_series_id=s.id)
	) AS q WHERE (?='all' OR kind=?)`
	args := []any{profileID, profileID, profileID, profileID, profileID, profileID, media, media}
	if policy.Restricted() {
		encoded, err := viewerPolicyArgument(policy)
		if err != nil {
			return nil, "", err
		}
		query += viewerPolicySQL
		args = append(args, encoded)
	}
	switch section {
	case "":
	case "Continue Watching":
		query += ` AND last_progress_at>0 AND completed=0 AND dismissed=0`
	case "My List":
		query += ` AND list_added>0`
	case "New":
	default:
		query += ` AND EXISTS (SELECT 1 FROM json_each(genres_json) WHERE value=?)`
		args = append(args, section)
	}
	orderText, orderNumber := "LOWER(title)", "0"
	descending := false
	switch mode {
	case "year":
		orderNumber, orderText, descending = "year", "LOWER(title)", true
	case "added":
		orderNumber, orderText, descending = "added_at", "''", true
	case "list":
		orderNumber, orderText, descending = "list_added", "''", true
	case "watched":
		orderNumber, orderText, descending = "last_progress_at", "''", true
	}
	if cursor.ID != "" {
		comparison := ">"
		if descending {
			comparison = "<"
		}
		if mode == "title" {
			query += ` AND (` + orderText + `>? OR (` + orderText + `=? AND (id>? OR (id=? AND kind>?))))`
			args = append(args, cursor.Text, cursor.Text, cursor.ID, cursor.ID, cursor.Kind)
		} else if mode == "year" {
			query += ` AND (` + orderNumber + comparison + `? OR (` + orderNumber + `=? AND (` + orderText + `>? OR (` + orderText + `=? AND (id>? OR (id=? AND kind>?))))))`
			args = append(args, cursor.Number, cursor.Number, cursor.Text, cursor.Text, cursor.ID, cursor.ID, cursor.Kind)
		} else {
			query += ` AND (` + orderNumber + comparison + `? OR (` + orderNumber + `=? AND (id>? OR (id=? AND kind>?))))`
			args = append(args, cursor.Number, cursor.Number, cursor.ID, cursor.ID, cursor.Kind)
		}
	}
	direction := "ASC"
	if descending {
		direction = "DESC"
	}
	if mode == "title" {
		query += ` ORDER BY ` + orderText + ` ASC,id ASC,kind ASC`
	} else if mode == "year" {
		query += ` ORDER BY ` + orderNumber + ` ` + direction + `,` + orderText + ` ASC,id ASC,kind ASC`
	} else {
		query += ` ORDER BY ` + orderNumber + ` ` + direction + `,id ASC,kind ASC`
	}
	query += ` LIMIT ?`
	args = append(args, limit+1)
	rows, err := c.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query viewer page: %w", err)
	}
	defer rows.Close()
	items := make([]viewerItem, 0, limit+1)
	for rows.Next() {
		item, err := scanViewerItem(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
		if len(items) > limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate viewer page: %w", err)
	}
	if len(items) <= limit {
		return publicItems(items), "", nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	next := binding
	next.ID, next.Kind = last.ID, last.Kind
	switch mode {
	case "title":
		next.Text = sqliteTitleKey(last.Title)
	case "year":
		next.Number, next.Text = int64(last.Year), sqliteTitleKey(last.Title)
	case "added":
		next.Number = last.AddedAt
	case "list":
		next.Number = last.listAdded
	case "watched":
		next.Number = last.lastProgress
	}
	return publicItems(items), encodeViewerCursor(next), nil
}

// SQLite's built-in LOWER folds ASCII only. Cursor keys must use the same
// normalization as ORDER BY and the keyset predicate, including Unicode input.
func sqliteTitleKey(title string) string {
	return strings.Map(func(value rune) rune {
		if value >= 'A' && value <= 'Z' {
			return value + ('a' - 'A')
		}
		return value
	}, title)
}

type viewerScanner interface{ Scan(...any) error }

func scanViewerItem(row viewerScanner) (viewerItem, error) {
	var item viewerItem
	var local, playable, demo, completed, dismissed int
	var genres string
	if err := row.Scan(&item.ID, &item.Kind, &item.Title, &local, &item.ProviderID, &item.Year, &item.Synopsis, &item.Poster, &item.Backdrop, &genres, &item.AddedAt, &playable, &demo, &item.lastProgress, &item.listAdded, &completed, &dismissed); err != nil {
		return viewerItem{}, fmt.Errorf("scan viewer catalog: %w", err)
	}
	if err := json.Unmarshal([]byte(genres), &item.Genres); err != nil {
		return viewerItem{}, fmt.Errorf("decode viewer genres: %w", err)
	}
	item.LocalOnly, item.Playable, item.Demo, item.completed, item.dismissed = local != 0, playable != 0, demo != 0, completed != 0, dismissed != 0
	return item, nil
}

func (c *Catalog) viewerGenres(ctx context.Context, media string, policy access.Policy) ([]string, error) {
	query := `SELECT DISTINCT genre.value FROM (
		SELECT id,genres_json,'film' kind FROM catalog_items WHERE series_id='' AND merged_into='' AND NOT EXISTS (SELECT 1 FROM catalog_film_version_memberships v WHERE v.member_catalog_id=catalog_items.id)
		UNION ALL SELECT id,genres_json,'series' kind FROM catalog_series WHERE merged_into='' AND NOT EXISTS (SELECT 1 FROM catalog_series_version_memberships v WHERE v.member_series_id=catalog_series.id)
	) AS q JOIN json_each(q.genres_json) AS genre WHERE genre.value<>'' AND (?='all' OR q.kind=?)`
	args := []any{media, media}
	if policy.Restricted() {
		encoded, err := viewerPolicyArgument(policy)
		if err != nil {
			return nil, err
		}
		query += viewerPolicySQL
		args = append(args, encoded)
	}
	query += ` ORDER BY LOWER(genre.value),genre.value LIMIT ?`
	args = append(args, maxViewerPageSize)
	rows, err := c.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query viewer genres: %w", err)
	}
	defer rows.Close()
	genres := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan viewer genre: %w", err)
		}
		genres = append(genres, name)
	}
	return genres, rows.Err()
}

func (c *Catalog) viewerItems(profileID, media string) ([]viewerItem, error) {
	if c.db == nil {
		return nil, ErrInvalidViewerMode
	}
	rows, err := c.db.Query(`SELECT id,kind,title,local_only,provider_id,year,synopsis,poster,backdrop,genres_json,added_at,playable,demo,last_progress_at,list_added,completed,dismissed FROM (
		SELECT i.id,'film' AS kind,i.title,i.local_only,i.provider_id,i.year,i.synopsis,i.poster,i.backdrop,i.genres_json,i.added_at,i.playable,i.demo,COALESCE(p.updated_at,0) AS last_progress_at,COALESCE(l.added_at,0) AS list_added,COALESCE(p.completed,0) AS completed,COALESCE(d.catalog_id IS NOT NULL,0) AS dismissed
		FROM catalog_items i LEFT JOIN progress p ON p.catalog_id=i.id AND p.profile_id=? LEFT JOIN profile_film_list l ON l.catalog_id=i.id AND l.profile_id=? LEFT JOIN profile_continue_watching_dismissals d ON d.profile_id=? AND d.catalog_kind='film' AND d.catalog_id=i.id WHERE i.series_id='' AND i.merged_into='' AND NOT EXISTS (SELECT 1 FROM catalog_film_version_memberships v WHERE v.member_catalog_id=i.id)
		UNION ALL
		SELECT s.id,'series' AS kind,s.title,s.local_only,s.provider_id,s.year,s.synopsis,s.poster,s.backdrop,s.genres_json,s.added_at,s.playable,s.demo,COALESCE(w.updated_at,0) AS last_progress_at,COALESCE(l.added_at,0) AS list_added,COALESCE(w.completed,0) AS completed,COALESCE(d.catalog_id IS NOT NULL,0) AS dismissed
		FROM catalog_series s LEFT JOIN (SELECT i.series_id,MAX(COALESCE(p.updated_at,0)) AS updated_at,MIN(COALESCE(p.completed,0)) AS completed FROM catalog_items i LEFT JOIN progress p ON p.catalog_id=i.id AND p.profile_id=? WHERE i.series_id<>'' GROUP BY i.series_id) w ON w.series_id=s.id LEFT JOIN profile_series_list l ON l.catalog_id=s.id AND l.profile_id=? LEFT JOIN profile_continue_watching_dismissals d ON d.profile_id=? AND d.catalog_kind='series' AND d.catalog_id=s.id WHERE s.merged_into='' AND NOT EXISTS (SELECT 1 FROM catalog_series_version_memberships v WHERE v.member_series_id=s.id)
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
	root, err := c.sourceRoot(item)
	if err != nil {
		return Item{}, ErrNotPlayable
	}
	item.sourceRoot = root
	return item, nil
}
