package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	return c.editMetadata(context.Background(), kind, id, edit, false)
}

func (c *Catalog) editMetadata(ctx context.Context, kind, id string, edit MetadataEdit, providerWrite bool) (Item, error) {
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
	if c.db == nil {
		return Item{}, errors.New("metadata persistence unavailable")
	}
	tx, err := c.db.Begin()
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
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
		if field.Field == "year" && field.Value != "" {
			if _, err := strconv.Atoi(field.Value); err != nil {
				return Item{}, errors.New("invalid year")
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
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
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
	edit, _, err := c.refreshEdit(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	return c.PreviewMetadata(kind, id, edit)
}

func (c *Catalog) refreshEdit(ctx context.Context, kind, id string) (MetadataEdit, map[string]Artwork, error) {
	c.mu.RLock()
	item, ok := c.metadataTarget(kind, id)
	provider, token := c.provider, c.token
	c.mu.RUnlock()
	if !ok {
		return MetadataEdit{}, nil, ErrMetadataNotFound
	}
	p, ok := provider.(CandidateProvider)
	if !ok || token == "" || item.ProviderID == "" {
		return MetadataEdit{}, nil, ErrProviderUnavailable
	}
	enrichment, err := p.ByID(ctx, token, kind, item.ProviderID, item.Language, item.Region)
	if err != nil {
		return MetadataEdit{}, nil, fmt.Errorf("refresh metadata: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return MetadataEdit{}, nil, err
	}
	artwork := c.stageMatchArtwork(ctx, enrichment)
	if err := ctx.Err(); err != nil {
		return MetadataEdit{}, nil, err
	}
	return refreshEdit(id, enrichment, artwork), artwork, nil
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
	edit, artwork, err := c.refreshEdit(ctx, kind, id)
	if err != nil {
		return Item{}, err
	}
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	fields, err := c.PreviewMetadata(kind, id, edit)
	if err != nil {
		return Item{}, err
	}
	updated, err := c.editMetadata(ctx, kind, id, MetadataEdit{Fields: fields}, true)
	if err != nil {
		return Item{}, err
	}
	c.publishRefreshArtwork(kind, id, fields, artwork)
	return c.metadataResult(kind, id, updated), nil
}

func (c *Catalog) publishRefreshArtwork(kind, id string, preview []MetadataField, artwork map[string]Artwork) {
	allowed := map[string]bool{}
	for _, field := range preview {
		if field.Source == "provider" && (field.Field == "poster" || field.Field == "backdrop") {
			allowed[field.Field] = true
		}
	}
	if len(allowed) == 0 {
		return
	}
	for imageKind, art := range artwork {
		if !allowed[imageKind] {
			continue
		}
		c.publishRefreshArtworkField(kind, id, imageKind, art)
	}
}

func (c *Catalog) publishRefreshArtworkField(kind, id, imageKind string, art Artwork) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.metadataTarget(kind, id)
	if !ok {
		return
	}
	var locked int
	if c.db == nil || c.db.QueryRow(`SELECT locked FROM catalog_metadata_fields WHERE catalog_kind=? AND catalog_id=? AND field=?`, kind, id, imageKind).Scan(&locked) == nil && locked != 0 {
		return
	}
	url, err := c.cacheArtwork(id, imageKind, art)
	if err != nil || url == "" {
		return
	}
	if imageKind == "poster" {
		item.Poster = url
	} else {
		item.Backdrop = url
	}
	if c.updateArtworkReferences(kind, item) != nil {
		return
	}
	if kind == "film" {
		c.items[id] = item
	} else {
		series := c.series[id]
		series.Poster, series.Backdrop = item.Poster, item.Backdrop
		c.series[id] = series
	}
}
