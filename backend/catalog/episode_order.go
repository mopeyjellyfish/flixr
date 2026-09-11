package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type EpisodeOrderSeries struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func (c *Catalog) EpisodeOrderSeries(query string, offset, limit int) ([]EpisodeOrderSeries, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	needle := strings.ToLower(strings.TrimSpace(query))
	all := make([]EpisodeOrderSeries, 0, len(c.series))
	for _, series := range c.series {
		if needle == "" || strings.Contains(strings.ToLower(series.Title), needle) {
			all = append(all, EpisodeOrderSeries{series.ID, series.Title})
		}
	}
	sort.Slice(all, func(i, j int) bool { return strings.ToLower(all[i].Title) < strings.ToLower(all[j].Title) })
	total := len(all)
	if offset < 0 || offset >= total {
		return []EpisodeOrderSeries{}, total
	}
	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}
	return all[offset : offset+limit], total
}

var (
	ErrEpisodeOrderInvalid  = errors.New("episode order is invalid")
	ErrEpisodeOrderConflict = errors.New("episode order revision conflicts")
)

// EpisodeOrderPosition is an owner-selected viewing position. Source episode
// coordinates on Item are never rewritten when this changes.
type EpisodeOrderPosition struct {
	Position    int  `json:"position"`
	EndPosition int  `json:"end_position"`
	Season      int  `json:"season"`
	Episode     int  `json:"episode"`
	EpisodeEnd  int  `json:"episode_end"`
	Special     bool `json:"special"`
}

type EpisodeOrderEntry struct {
	CatalogID       string                `json:"catalog_id"`
	Title           string                `json:"title"`
	Season          int                   `json:"season"`
	Episode         int                   `json:"episode"`
	EpisodeEnd      int                   `json:"episode_end"`
	AbsoluteEpisode int                   `json:"absolute_episode,omitempty"`
	Mapping         *EpisodeOrderPosition `json:"mapping,omitempty"`
}

type EpisodeOrderDetail struct {
	SeriesID    string              `json:"series_id"`
	Title       string              `json:"title"`
	Order       string              `json:"order"`
	Revision    int                 `json:"revision"`
	NeedsRepair bool                `json:"needs_repair"`
	Entries     []EpisodeOrderEntry `json:"entries"`
}

type EpisodeOrderGroup struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order string `json:"order"`
}

type ProviderEpisodeOrderGroup struct {
	EpisodeOrderGroup
	Episodes []ProviderEpisodeOrderEntry
}

// ProviderEpisodeOrderEntry separates a provider's source coordinate (used to
// reconcile local filenames) from the display/sequence mapping it supplies.
type ProviderEpisodeOrderEntry struct {
	SourceSeason    int
	SourceEpisode   int
	AbsoluteEpisode int
	Mapping         EpisodeOrderPosition
}

// EpisodeOrderProvider is optional: startup and playback never contact it.
type EpisodeOrderProvider interface {
	EpisodeOrderGroups(context.Context, string, string) ([]EpisodeOrderGroup, error)
	EpisodeOrderGroup(context.Context, string, string) (ProviderEpisodeOrderGroup, error)
}

func (c *Catalog) loadEpisodeSpans() error {
	if c.db == nil {
		return nil
	}
	rows, err := c.db.Query(`SELECT catalog_id,source_season,source_start,source_end,absolute_episode FROM catalog_episode_spans`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var season, start, end, absolute int
		if err := rows.Scan(&id, &season, &start, &end, &absolute); err != nil {
			return err
		}
		if item, ok := c.items[id]; ok && item.Kind == "episode" {
			item.Season, item.Episode, item.EpisodeEnd, item.AbsoluteEpisode = season, start, end, absolute
			c.items[id] = item
		}
	}
	return rows.Err()
}

func validOrder(kind string) bool { return kind == "aired" || kind == "dvd" || kind == "absolute" }

// EpisodeOrder returns the saved owner mapping without contacting a provider.
func (c *Catalog) EpisodeOrder(seriesID string) (EpisodeOrderDetail, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.episodeOrderLocked(seriesID)
}

// episodeOrderLocked requires c.mu to be held for reading or writing. It
// snapshots the in-memory identity and reads header/mapping as one serialized
// catalog operation relative to owner saves and identity merges.
func (c *Catalog) episodeOrderLocked(seriesID string) (EpisodeOrderDetail, error) {
	series, ok := c.series[seriesID]
	items := make([]Item, 0)
	for _, item := range c.items {
		if item.Kind == "episode" && item.SeriesID == seriesID {
			items = append(items, item)
		}
	}
	if !ok {
		return EpisodeOrderDetail{}, ErrCatalogNotFound
	}
	detail := EpisodeOrderDetail{SeriesID: seriesID, Title: series.Title, Order: "aired", Entries: make([]EpisodeOrderEntry, 0, len(items))}
	if c.db != nil {
		var repair int
		err := c.db.QueryRow(`SELECT order_kind,revision,needs_repair FROM catalog_episode_orders WHERE series_id=?`, seriesID).Scan(&detail.Order, &detail.Revision, &repair)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return EpisodeOrderDetail{}, err
		}
		detail.NeedsRepair = repair != 0
		rows, err := c.db.Query(`SELECT catalog_id,position,end_position,display_season,display_episode,display_episode_end,special FROM catalog_episode_order_items WHERE series_id=?`, seriesID)
		if err != nil {
			return EpisodeOrderDetail{}, err
		}
		mapped := map[string]EpisodeOrderPosition{}
		for rows.Next() {
			var id string
			var p EpisodeOrderPosition
			var special int
			if err := rows.Scan(&id, &p.Position, &p.EndPosition, &p.Season, &p.Episode, &p.EpisodeEnd, &special); err != nil {
				rows.Close()
				return EpisodeOrderDetail{}, err
			}
			p.Special = special != 0
			mapped[id] = p
		}
		if err := rows.Close(); err != nil {
			return EpisodeOrderDetail{}, err
		}
		for i := range items {
			if p, ok := mapped[items[i].ID]; ok {
				items[i].EpisodeOrder = &p
			}
		}
		if detail.Revision > 0 && (detail.Order != "aired" || len(mapped) != 0) && len(mapped) != len(items) {
			detail.NeedsRepair = true
		}
	}
	for _, item := range items {
		detail.Entries = append(detail.Entries, EpisodeOrderEntry{CatalogID: item.ID, Title: item.Title, Season: item.Season, Episode: item.Episode, EpisodeEnd: item.EpisodeEnd, AbsoluteEpisode: item.AbsoluteEpisode, Mapping: item.EpisodeOrder})
	}
	sort.Slice(detail.Entries, func(i, j int) bool { return detail.Entries[i].CatalogID < detail.Entries[j].CatalogID })
	return detail, nil
}

// SaveEpisodeOrder atomically replaces the selected order. Empty aired entries
// explicitly restore source numbering; incomplete entries are repair-required.
func (c *Catalog) SaveEpisodeOrder(ctx context.Context, seriesID, order string, revision int, entries []EpisodeOrderEntry) (EpisodeOrderDetail, error) {
	if !validOrder(order) || revision < 0 {
		return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	detail, err := c.episodeOrderLocked(seriesID)
	if err != nil {
		return EpisodeOrderDetail{}, err
	}
	if detail.Revision != revision {
		return EpisodeOrderDetail{}, ErrEpisodeOrderConflict
	}
	if c.db == nil {
		return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
	}
	var activeMerge int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM catalog_identity_merges WHERE kind='series' AND state='active' AND (survivor_catalog_id=? OR source_catalog_id=?)`, seriesID, seriesID).Scan(&activeMerge); err != nil || activeMerge != 0 {
		return EpisodeOrderDetail{}, ErrEpisodeOrderConflict
	}
	known := map[string]EpisodeOrderEntry{}
	for _, entry := range detail.Entries {
		known[entry.CatalogID] = entry
	}
	seen := map[string]bool{}
	repair := false
	intervals := map[string][]EpisodeOrderPosition{}
	for _, entry := range entries {
		if _, ok := known[entry.CatalogID]; !ok || seen[entry.CatalogID] {
			return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
		}
		seen[entry.CatalogID] = true
		if entry.Mapping == nil {
			repair = true
			continue
		}
		p := entry.Mapping
		if p.Position < 1 || p.EndPosition < p.Position || p.Season < 0 || p.Episode < 1 || p.EpisodeEnd < p.Episode {
			return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
		}
		source := known[entry.CatalogID]
		if p.EndPosition-p.Position != source.EpisodeEnd-source.Episode || p.EpisodeEnd-p.Episode != source.EpisodeEnd-source.Episode {
			return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
		}
		item := c.items[entry.CatalogID]
		context := episodeSequenceContext(item)
		for _, other := range intervals[context] {
			if p.Position <= other.EndPosition && other.Position <= p.EndPosition {
				repair = true
			}
		}
		intervals[context] = append(intervals[context], *p)
	}
	if (order != "aired" || len(entries) != 0) && len(entries) != len(known) {
		repair = true
	}
	if order == "aired" && len(entries) == 0 {
		repair = false
	}
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return EpisodeOrderDetail{}, err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM catalog_episode_orders WHERE series_id=?`, seriesID).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return EpisodeOrderDetail{}, err
	}
	if current != revision {
		return EpisodeOrderDetail{}, ErrEpisodeOrderConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_episode_orders(series_id,order_kind,revision,needs_repair) VALUES(?,?,?,?) ON CONFLICT(series_id) DO UPDATE SET order_kind=excluded.order_kind,revision=excluded.revision,needs_repair=excluded.needs_repair`, seriesID, order, revision+1, boolInt(repair)); err != nil {
		return EpisodeOrderDetail{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_episode_order_items WHERE series_id=?`, seriesID); err != nil {
		return EpisodeOrderDetail{}, err
	}
	for _, entry := range entries {
		if entry.Mapping == nil {
			continue
		}
		p := entry.Mapping
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_episode_order_items(series_id,catalog_id,position,end_position,display_season,display_episode,display_episode_end,special) VALUES(?,?,?,?,?,?,?,?)`, seriesID, entry.CatalogID, p.Position, p.EndPosition, p.Season, p.Episode, p.EpisodeEnd, boolInt(p.Special)); err != nil {
			return EpisodeOrderDetail{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return EpisodeOrderDetail{}, fmt.Errorf("save episode order: %w", err)
	}
	return c.episodeOrderLocked(seriesID)
}

func (c *Catalog) EpisodeOrderGroups(ctx context.Context, seriesID string) ([]EpisodeOrderGroup, error) {
	c.mu.RLock()
	series, ok := c.series[seriesID]
	provider, token, enabled := c.provider, "", c.metadataEnabled
	if resolved, _ := c.effectiveTMDBTokenLocked(); resolved != "" {
		token = resolved
	}
	c.mu.RUnlock()
	if !ok {
		return nil, ErrCatalogNotFound
	}
	p, ok := provider.(EpisodeOrderProvider)
	if !ok || !enabled || token == "" || series.ProviderID == "" {
		return nil, ErrProviderUnavailable
	}
	return p.EpisodeOrderGroups(ctx, token, series.ProviderID)
}

func (c *Catalog) PreviewEpisodeOrder(ctx context.Context, seriesID, groupID string) (EpisodeOrderDetail, error) {
	c.mu.RLock()
	series, ok := c.series[seriesID]
	provider, token, enabled := c.provider, "", c.metadataEnabled
	if resolved, _ := c.effectiveTMDBTokenLocked(); resolved != "" {
		token = resolved
	}
	c.mu.RUnlock()
	if !ok {
		return EpisodeOrderDetail{}, ErrCatalogNotFound
	}
	p, ok := provider.(EpisodeOrderProvider)
	if !ok || !enabled || token == "" || series.ProviderID == "" {
		return EpisodeOrderDetail{}, ErrProviderUnavailable
	}
	groups, err := p.EpisodeOrderGroups(ctx, token, series.ProviderID)
	if err != nil {
		return EpisodeOrderDetail{}, err
	}
	member := false
	for _, candidate := range groups {
		if candidate.ID == groupID {
			member = true
			break
		}
	}
	if !member {
		return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
	}
	group, err := p.EpisodeOrderGroup(ctx, token, groupID)
	if err != nil {
		return EpisodeOrderDetail{}, err
	}
	if group.ID != groupID {
		return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
	}
	if !validOrder(group.Order) {
		return EpisodeOrderDetail{}, ErrEpisodeOrderInvalid
	}
	detail, err := c.EpisodeOrder(seriesID)
	if err != nil {
		return EpisodeOrderDetail{}, err
	}
	detail.Order = group.Order
	detail.NeedsRepair = false
	for i := range detail.Entries {
		detail.Entries[i].Mapping = nil
	}
	bySource := map[string][]ProviderEpisodeOrderEntry{}
	for _, p := range group.Episodes {
		key := fmt.Sprintf("%d/%d", p.SourceSeason, p.SourceEpisode)
		bySource[key] = append(bySource[key], p)
		if group.Order == "absolute" && p.AbsoluteEpisode > 0 {
			absolute := fmt.Sprintf("absolute/%d", p.AbsoluteEpisode)
			bySource[absolute] = append(bySource[absolute], p)
		}
	}
	for i := range detail.Entries {
		entry := &detail.Entries[i]
		var span []ProviderEpisodeOrderEntry
		for offset := 0; offset <= entry.EpisodeEnd-entry.Episode; offset++ {
			key := fmt.Sprintf("%d/%d", entry.Season, entry.Episode+offset)
			if group.Order == "absolute" && entry.AbsoluteEpisode > 0 {
				key = fmt.Sprintf("absolute/%d", entry.AbsoluteEpisode+offset)
			}
			candidates := bySource[key]
			if len(candidates) != 1 {
				span = nil
				break
			}
			span = append(span, candidates[0])
		}
		if len(span) == 0 {
			detail.NeedsRepair = true
			continue
		}
		first, last := span[0].Mapping, span[len(span)-1].Mapping
		valid := first.Position > 0
		for offset, providerEntry := range span {
			mapped := providerEntry.Mapping
			if mapped.Position != first.Position+offset || mapped.Season != first.Season || mapped.Episode != first.Episode+offset || mapped.Special != first.Special {
				valid = false
				break
			}
		}
		if !valid || last.Position-first.Position != len(span)-1 || first.Season != last.Season || last.Episode-first.Episode != len(span)-1 {
			detail.NeedsRepair = true
			continue
		}
		entry.Mapping = &EpisodeOrderPosition{Position: first.Position, EndPosition: last.Position, Season: first.Season, Episode: first.Episode, EpisodeEnd: last.Episode, Special: first.Special}
	}
	return detail, nil
}
