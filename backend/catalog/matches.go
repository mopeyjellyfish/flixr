package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrMetadataNotFound    = errors.New("catalog metadata target not found")
	ErrProviderUnavailable = errors.New("metadata provider is unavailable")
	ErrMetadataBusy        = errors.New("metadata repair is unavailable during a scan")
)

// Candidate is a provider result an owner may explicitly attach to local media.
type Candidate struct {
	Provider   string  `json:"provider"`
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Year       int     `json:"year,omitempty"`
	Language   string  `json:"language,omitempty"`
	Region     string  `json:"region,omitempty"`
	Confidence float64 `json:"confidence"`
}

type CandidateProvider interface {
	Candidates(context.Context, string, string, string, string, string) ([]Candidate, error)
	ByID(context.Context, string, string, string, string, string) (Enrichment, error)
}

func (c *Catalog) MetadataTargets() []Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Item, 0)
	for _, item := range c.items {
		if item.Kind == "film" {
			out = append(out, publicMetadataItem(item))
		}
	}
	for _, series := range c.series {
		out = append(out, publicMetadataItem(Item{ID: series.ID, Title: series.Title, Kind: "series", LocalOnly: series.LocalOnly, ProviderID: series.ProviderID, Provider: series.Provider, Language: series.Language, Region: series.Region, Confidence: series.Confidence, OwnerMatch: series.OwnerMatch, OwnerUnmatch: series.OwnerUnmatch, Year: series.Year, Synopsis: series.Synopsis, Poster: series.Poster, Backdrop: series.Backdrop}))
	}
	return out
}

func (c *Catalog) Unmatched() []Item { return c.MetadataTargets() }

func publicMetadataItem(item Item) Item {
	item.path, item.rootKind, item.fingerprint = "", "", ""
	return item
}

func (c *Catalog) Candidates(ctx context.Context, kind, id, query, language, region string) ([]Candidate, error) {
	c.mu.RLock()
	item, ok := c.metadataTarget(kind, id)
	provider, token := c.provider, c.token
	c.mu.RUnlock()
	if !ok {
		return nil, ErrMetadataNotFound
	}
	p, ok := provider.(CandidateProvider)
	if !ok || token == "" {
		return nil, ErrProviderUnavailable
	}
	if strings.TrimSpace(query) == "" {
		query = item.Title
	}
	return p.Candidates(ctx, token, kind, query, language, region)
}

func (c *Catalog) Match(ctx context.Context, kind, id, providerID, language, region string) (Item, error) {
	c.mu.RLock()
	_, ok := c.metadataTarget(kind, id)
	provider, token := c.provider, c.token
	c.mu.RUnlock()
	if !ok {
		return Item{}, ErrMetadataNotFound
	}
	p, ok := provider.(CandidateProvider)
	if !ok || token == "" {
		return Item{}, ErrProviderUnavailable
	}
	if strings.TrimSpace(providerID) == "" {
		return Item{}, errors.New("provider identifier is required")
	}
	enrichment, err := p.ByID(ctx, token, kind, providerID, language, region)
	if err != nil {
		return Item{}, fmt.Errorf("fetch selected metadata: %w", err)
	}
	if enrichment.ProviderID == "" {
		return Item{}, ErrProviderUnavailable
	}
	artwork := c.stageMatchArtwork(ctx, enrichment)
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	// Provider paths are never published. They become local URLs only after this owner choice commits.
	enrichment.Poster, enrichment.Backdrop = "", ""
	matched, err := c.saveMatch(kind, id, enrichment, language, region, true)
	if err != nil {
		return Item{}, err
	}
	c.publishMatchArtwork(kind, id, enrichment.ProviderID, language, region, artwork)
	return c.metadataResult(kind, id, matched), nil
}

func (c *Catalog) stageMatchArtwork(ctx context.Context, enrichment Enrichment) map[string]Artwork {
	c.mu.RLock()
	provider, ok := c.provider.(ArtworkProvider)
	c.mu.RUnlock()
	if !ok || c.db == nil {
		return nil
	}
	out := map[string]Artwork{}
	for kind, imagePath := range map[string]string{"poster": enrichment.Poster, "backdrop": enrichment.Backdrop} {
		if imagePath == "" || ctx.Err() != nil {
			continue
		}
		if art, err := provider.FetchArtwork(ctx, imagePath); err == nil && len(art.Bytes) > 0 && allowedArtworkContentType(art.ContentType) {
			out[kind] = art
		}
	}
	return out
}

func (c *Catalog) publishMatchArtwork(kind, id, providerID, language, region string, artwork map[string]Artwork) {
	if len(artwork) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.metadataTarget(kind, id)
	if !ok || !item.OwnerMatch || item.ProviderID != providerID || item.Language != language || item.Region != region {
		return
	}
	for imageKind, art := range artwork {
		url, err := c.cacheArtwork(id, imageKind, art)
		if err != nil || url == "" {
			continue
		}
		if imageKind == "poster" {
			item.Poster = url
		} else {
			item.Backdrop = url
		}
	}
	if err := c.updateArtworkReferences(kind, item); err != nil {
		return
	}
	if kind == "film" {
		c.items[id] = item
		return
	}
	series := c.series[id]
	series.Poster, series.Backdrop = item.Poster, item.Backdrop
	c.series[id] = series
}

func (c *Catalog) metadataResult(kind, id string, fallback Item) Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if item, ok := c.metadataTarget(kind, id); ok {
		return publicMetadataItem(item)
	}
	return fallback
}

func (c *Catalog) updateArtworkReferences(kind string, item Item) error {
	if c.db == nil {
		return nil
	}
	table := "catalog_items"
	if kind == "series" {
		table = "catalog_series"
	}
	_, err := c.db.Exec(`UPDATE `+table+` SET poster=?,backdrop=? WHERE id=?`, item.Poster, item.Backdrop, item.ID)
	return err
}

func (c *Catalog) Unmatch(kind, id string) (Item, error) {
	return c.saveMatch(kind, id, Enrichment{}, "", "", false)
}

func (c *Catalog) metadataTarget(kind, id string) (Item, bool) {
	if kind == "film" {
		item, ok := c.items[id]
		return item, ok && item.Kind == "film"
	}
	if kind == "series" {
		series, ok := c.series[id]
		return Item{ID: series.ID, Title: series.Title, Kind: "series", LocalOnly: series.LocalOnly, ProviderID: series.ProviderID, Provider: series.Provider, Language: series.Language, Region: series.Region, Confidence: series.Confidence, OwnerMatch: series.OwnerMatch, OwnerUnmatch: series.OwnerUnmatch, Year: series.Year, Synopsis: series.Synopsis, Poster: series.Poster, Backdrop: series.Backdrop}, ok
	}
	return Item{}, false
}

func (c *Catalog) saveMatch(kind, id string, enrichment Enrichment, language, region string, owner bool) (Item, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return Item{}, ErrMetadataBusy
	}
	if kind == "film" {
		item, ok := c.items[id]
		if !ok || item.Kind != "film" {
			return Item{}, ErrMetadataNotFound
		}
		applyMatch(&item, enrichment, language, region, owner)
		if err := c.updateMatch(kind, item); err != nil {
			return Item{}, err
		}
		c.items[id] = item
		return publicMetadataItem(item), nil
	}
	if kind == "series" {
		series, ok := c.series[id]
		if !ok {
			return Item{}, ErrMetadataNotFound
		}
		item := Item{ID: series.ID, Title: series.Title, Kind: "series", ProviderID: series.ProviderID, Provider: series.Provider, Language: series.Language, Region: series.Region, Confidence: series.Confidence, OwnerMatch: series.OwnerMatch, OwnerUnmatch: series.OwnerUnmatch, Year: series.Year, Synopsis: series.Synopsis, Poster: series.Poster, Backdrop: series.Backdrop}
		applyMatch(&item, enrichment, language, region, owner)
		series.ProviderID, series.Provider, series.Language, series.Region, series.Confidence, series.OwnerMatch, series.OwnerUnmatch, series.Year, series.Synopsis, series.Poster, series.Backdrop = item.ProviderID, item.Provider, item.Language, item.Region, item.Confidence, item.OwnerMatch, item.OwnerUnmatch, item.Year, item.Synopsis, item.Poster, item.Backdrop
		series.LocalOnly = !owner
		if err := c.updateMatch(kind, item); err != nil {
			return Item{}, err
		}
		c.series[id] = series
		return item, nil
	}
	return Item{}, ErrMetadataNotFound
}

func applyMatch(item *Item, enrichment Enrichment, language, region string, owner bool) {
	item.ProviderID, item.Year, item.Synopsis, item.Poster, item.Backdrop = enrichment.ProviderID, enrichment.Year, enrichment.Synopsis, enrichment.Poster, enrichment.Backdrop
	item.Provider, item.Language, item.Region, item.Confidence, item.OwnerMatch, item.OwnerUnmatch = "tmdb", language, region, 1, owner, !owner
	if !owner {
		item.Provider, item.Language, item.Region, item.Confidence = "", "", "", 0
		item.LocalOnly = true
	} else {
		item.LocalOnly = false
	}
}

func (c *Catalog) updateMatch(kind string, item Item) error {
	if c.db == nil {
		return nil
	}
	table := "catalog_items"
	if kind == "series" {
		table = "catalog_series"
	}
	_, err := c.db.Exec(`UPDATE `+table+` SET provider_id=?,metadata_provider=?,metadata_language=?,metadata_region=?,match_confidence=?,owner_matched=?,owner_unmatched=?,year=?,synopsis=?,poster=?,backdrop=?,local_only=? WHERE id=?`, item.ProviderID, item.Provider, item.Language, item.Region, item.Confidence, boolInt(item.OwnerMatch), boolInt(item.OwnerUnmatch), item.Year, item.Synopsis, item.Poster, item.Backdrop, boolInt(item.LocalOnly), item.ID)
	return err
}
