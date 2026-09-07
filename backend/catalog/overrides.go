package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type MetadataField struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Locked bool   `json:"locked"`
}
type MetadataEdit struct {
	Fields []MetadataField `json:"fields"`
}
type refreshPreview struct {
	edit     MetadataEdit
	artwork  map[string]Artwork
	expected Item
	expires  time.Time
}

const maxRefreshPreviews = 4

func refreshKey(kind, id string) string { return kind + ":" + id }

var editableMetadataFields = map[string]bool{"title": true, "synopsis": true, "year": true, "poster": true, "backdrop": true, "tags": true, "content_rating": true}

func (c *Catalog) MetadataFields(kind, id string) ([]MetadataField, error) {
	c.mu.RLock()
	_, ok := c.metadataTarget(kind, id)
	c.mu.RUnlock()
	if !ok {
		return nil, ErrMetadataNotFound
	}
	if c.db == nil {
		return []MetadataField{}, nil
	}
	rows, err := c.db.Query(`SELECT field,value,source,locked FROM catalog_metadata_fields WHERE catalog_kind=? AND catalog_id=? ORDER BY field`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetadataField{}
	for rows.Next() {
		var f MetadataField
		var locked int
		if err := rows.Scan(&f.Field, &f.Value, &f.Source, &locked); err != nil {
			return nil, err
		}
		f.Locked = locked != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

func (c *Catalog) EditMetadata(kind, id string, edit MetadataEdit) (Item, error) {
	return c.editMetadata(context.Background(), kind, id, edit, false, nil, nil)
}

func (c *Catalog) editMetadata(ctx context.Context, kind, id string, edit MetadataEdit, providerWrite bool, expected *Item, artwork map[string]Artwork) (Item, error) {
	if len(edit.Fields) == 0 {
		return Item{}, errors.New("metadata fields are required")
	}
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	if c.scanning {
		return Item{}, ErrMetadataBusy
	}
	item, ok := c.metadataTarget(kind, id)
	if !ok {
		return Item{}, ErrMetadataNotFound
	}
	if expected != nil && !sameMetadataIdentity(item, *expected) {
		return Item{}, ErrMetadataStale
	}
	if c.db == nil {
		return Item{}, errors.New("metadata persistence unavailable")
	}
	c.artworkMu.Lock()
	defer c.artworkMu.Unlock()
	var created, replaced []string
	committed := false
	defer func() {
		cleanup := created
		if committed {
			cleanup = replaced
		}
		for _, name := range cleanup {
			c.removeArtworkObject(name)
		}
	}()
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	if providerWrite {
		if _, err := tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=?`, kind, id); err != nil {
			return Item{}, err
		}
		if _, err := tx.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind=? AND catalog_id=?`, kind, id); err != nil {
			return Item{}, err
		}
	}
	seen := map[string]bool{}
	for _, field := range edit.Fields {
		field.Field = strings.TrimSpace(field.Field)
		if !editableMetadataFields[field.Field] || seen[field.Field] || (field.Source != "owner" && field.Source != "local" && (!providerWrite || field.Source != "provider")) {
			return Item{}, errors.New("invalid metadata field")
		}
		seen[field.Field] = true
		if providerWrite {
			var locked int
			err := tx.QueryRow(`SELECT locked FROM catalog_metadata_fields WHERE catalog_kind=? AND catalog_id=? AND field=?`, kind, id, field.Field).Scan(&locked)
			if err != nil && err != sql.ErrNoRows {
				return Item{}, err
			}
			if locked != 0 {
				continue
			}
		}
		if art, ok := artwork[field.Field]; ok && field.Source == "provider" {
			name, previous, err := c.replaceArtwork(tx, id, field.Field, art)
			if err != nil {
				return Item{}, err
			}
			created, replaced = append(created, name), append(replaced, previous)
		}
		if field.Field == "year" && field.Value != "" {
			if _, err := strconv.Atoi(field.Value); err != nil {
				return Item{}, errors.New("invalid year")
			}
		}
		if !providerWrite && (field.Field == "poster" || field.Field == "backdrop") {
			if _, err := tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, kind, id, field.Field); err != nil {
				return Item{}, err
			}
		}
		if _, err := tx.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES(?,?,?,?,?,?) ON CONFLICT(catalog_kind,catalog_id,field) DO UPDATE SET value=excluded.value,source=excluded.source,locked=excluded.locked`, kind, id, field.Field, field.Value, field.Source, boolInt(field.Locked)); err != nil {
			return Item{}, err
		}
		switch field.Field {
		case "title":
			item.Title = field.Value
		case "synopsis":
			item.Synopsis = field.Value
		case "year":
			if field.Value == "" {
				item.Year = 0
			} else {
				item.Year, _ = strconv.Atoi(field.Value)
			}
		case "poster":
			item.Poster = field.Value
		case "backdrop":
			item.Backdrop = field.Value
		}
	}
	table := "catalog_items"
	if kind == "series" {
		table = "catalog_series"
	}
	if _, err := tx.Exec(`UPDATE `+table+` SET title=?,synopsis=?,year=?,poster=?,backdrop=? WHERE id=?`, item.Title, item.Synopsis, item.Year, item.Poster, item.Backdrop, id); err != nil {
		return Item{}, err
	}
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	committed = true
	if kind == "film" {
		c.items[id] = item
	} else {
		series := c.series[id]
		series.Title, series.Synopsis, series.Year, series.Poster, series.Backdrop = item.Title, item.Synopsis, item.Year, item.Poster, item.Backdrop
		c.series[id] = series
	}
	return publicMetadataItem(item), nil
}

func (c *Catalog) PreviewMetadata(kind, id string, edit MetadataEdit) ([]MetadataField, error) {
	current, err := c.MetadataFields(kind, id)
	if err != nil {
		return nil, err
	}
	known := map[string]MetadataField{}
	for _, f := range current {
		known[f.Field] = f
	}
	for _, f := range edit.Fields {
		if !editableMetadataFields[f.Field] {
			return nil, fmt.Errorf("invalid metadata field")
		}
		if old, ok := known[f.Field]; ok && old.Locked {
			continue
		}
		known[f.Field] = f
	}
	out := make([]MetadataField, 0, len(known))
	for _, f := range known {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out, nil
}

func (c *Catalog) applyLockedFields(kind, id string, item *Item) {
	fields, err := c.MetadataFields(kind, id)
	if err != nil {
		return
	}
	for _, f := range fields {
		if !f.Locked {
			continue
		}
		switch f.Field {
		case "title":
			item.Title = f.Value
		case "synopsis":
			item.Synopsis = f.Value
		case "year":
			item.Year, _ = strconv.Atoi(f.Value)
		case "poster":
			item.Poster = f.Value
		case "backdrop":
			item.Backdrop = f.Value
		}
	}
}

func (c *Catalog) RefreshPreview(ctx context.Context, kind, id string) ([]MetadataField, error) {
	edit, artwork, expected, err := c.refreshEdit(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	fields, err := c.PreviewMetadata(kind, id, edit)
	if err == nil {
		c.mu.Lock()
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		current, ok := c.metadataTarget(kind, id)
		if !ok || !sameMetadataIdentity(current, expected) {
			c.mu.Unlock()
			return nil, ErrMetadataStale
		}
		now := time.Now()
		for key, preview := range c.refreshPreviews {
			if !now.Before(preview.expires) {
				delete(c.refreshPreviews, key)
			}
		}
		key := refreshKey(kind, id)
		if _, exists := c.refreshPreviews[key]; !exists && len(c.refreshPreviews) >= maxRefreshPreviews {
			var oldest string
			var expiry time.Time
			for candidate, preview := range c.refreshPreviews {
				if oldest == "" || preview.expires.Before(expiry) {
					oldest, expiry = candidate, preview.expires
				}
			}
			delete(c.refreshPreviews, oldest)
		}
		c.refreshPreviews[key] = refreshPreview{edit, artwork, expected, now.Add(time.Minute)}
		c.mu.Unlock()
	}
	return fields, err
}

func (c *Catalog) refreshEdit(ctx context.Context, kind, id string) (MetadataEdit, map[string]Artwork, Item, error) {
	c.mu.RLock()
	item, ok := c.metadataTarget(kind, id)
	provider, token := c.provider, c.token
	c.mu.RUnlock()
	if !ok {
		return MetadataEdit{}, nil, Item{}, ErrMetadataNotFound
	}
	p, ok := provider.(CandidateProvider)
	if !ok || token == "" || item.ProviderID == "" {
		return MetadataEdit{}, nil, Item{}, ErrProviderUnavailable
	}
	enrichment, err := p.ByID(ctx, token, kind, item.ProviderID, item.Language, item.Region)
	if err != nil {
		return MetadataEdit{}, nil, Item{}, fmt.Errorf("refresh metadata: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return MetadataEdit{}, nil, Item{}, err
	}
	artwork := c.stageMatchArtwork(ctx, enrichment)
	if err := ctx.Err(); err != nil {
		return MetadataEdit{}, nil, Item{}, err
	}
	for imageKind, path := range map[string]string{"poster": enrichment.Poster, "backdrop": enrichment.Backdrop} {
		if path != "" {
			if _, ok := artwork.available[imageKind]; !ok {
				return MetadataEdit{}, nil, Item{}, ErrProviderUnavailable
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return MetadataEdit{}, nil, Item{}, err
	}
	return refreshEdit(id, enrichment, artwork.available), artwork.available, item, nil
}

func refreshEdit(id string, enrichment Enrichment, artwork map[string]Artwork) MetadataEdit {
	fields := []MetadataField{{Field: "synopsis", Value: enrichment.Synopsis, Source: "provider"}, {Field: "year", Value: strconv.Itoa(enrichment.Year), Source: "provider"}}
	if enrichment.Title != "" {
		fields = append(fields, MetadataField{Field: "title", Value: enrichment.Title, Source: "provider"})
	}
	for _, kind := range []string{"poster", "backdrop"} {
		if _, ok := artwork[kind]; ok {
			fields = append(fields, MetadataField{Field: kind, Value: artworkURL(id, kind), Source: "provider"})
		}
	}
	return MetadataEdit{Fields: fields}
}

func (c *Catalog) Refresh(ctx context.Context, kind, id string) (Item, error) {
	c.mu.Lock()
	staged, ok := c.refreshPreviews[refreshKey(kind, id)]
	if ok {
		delete(c.refreshPreviews, refreshKey(kind, id))
	}
	c.mu.Unlock()
	edit, artwork, expected := staged.edit, staged.artwork, staged.expected
	var err error
	if !ok || time.Now().After(staged.expires) {
		edit, artwork, expected, err = c.refreshEdit(ctx, kind, id)
		if err != nil {
			return Item{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	updated, err := c.editMetadata(ctx, kind, id, edit, true, &expected, artwork)
	if err != nil {
		return Item{}, err
	}
	return c.metadataResult(kind, id, updated), nil
}

func sameMetadataIdentity(item, expected Item) bool {
	return item.metadataVersion == expected.metadataVersion && item.ProviderID == expected.ProviderID && item.Language == expected.Language && item.Region == expected.Region && item.OwnerMatch == expected.OwnerMatch && item.OwnerUnmatch == expected.OwnerUnmatch
}
