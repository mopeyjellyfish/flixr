package catalog

import (
	"errors"
	"fmt"
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
	if len(edit.Fields) == 0 {
		return Item{}, errors.New("metadata fields are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
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
	for _, field := range edit.Fields {
		field.Field = strings.TrimSpace(field.Field)
		if !editableMetadataFields[field.Field] || (field.Source != "owner" && field.Source != "local") {
			return Item{}, errors.New("invalid metadata field")
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
