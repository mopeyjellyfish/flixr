package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrMetadataNotFound    = errors.New("catalog metadata target not found")
	ErrProviderUnavailable = errors.New("metadata provider is unavailable")
	ErrMetadataBusy        = errors.New("metadata repair is unavailable during a scan")
	ErrMetadataStale       = errors.New("metadata changed while refresh was in progress")
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

type stagedMatchArtwork struct {
	available map[string]Artwork
	pending   map[string]string
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
	provider, token := c.provider, ""
	if resolved, _ := c.effectiveTMDBTokenLocked(); resolved != "" {
		token = resolved
	}
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
	expected, ok := c.metadataTarget(kind, id)
	provider, token := c.provider, ""
	if resolved, _ := c.effectiveTMDBTokenLocked(); resolved != "" {
		token = resolved
	}
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
	matched, err := c.saveMatch(kind, id, enrichment, language, region, true, &expected, artwork)
	if err != nil {
		return Item{}, err
	}
	return c.metadataResult(kind, id, matched), nil
}

func (c *Catalog) stageMatchArtwork(ctx context.Context, enrichment Enrichment) stagedMatchArtwork {
	c.mu.RLock()
	provider, ok := c.provider.(ArtworkProvider)
	c.mu.RUnlock()
	if !ok || c.db == nil {
		return stagedMatchArtwork{}
	}
	out := stagedMatchArtwork{available: map[string]Artwork{}, pending: map[string]string{}}
	for kind, imagePath := range map[string]string{"poster": enrichment.Poster, "backdrop": enrichment.Backdrop} {
		if imagePath == "" || ctx.Err() != nil {
			continue
		}
		if art, err := provider.FetchArtwork(ctx, imagePath); err == nil && len(art.Bytes) > 0 && len(art.Bytes) <= maxArtworkSource && allowedArtworkContentType(art.ContentType) {
			out.available[kind] = art
		} else {
			out.pending[kind] = imagePath
		}
	}
	return out
}

func (c *Catalog) metadataResult(kind, id string, fallback Item) Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if item, ok := c.metadataTarget(kind, id); ok {
		return publicMetadataItem(item)
	}
	return fallback
}

func (c *Catalog) Unmatch(kind, id string) (Item, error) {
	return c.saveMatch(kind, id, Enrichment{}, "", "", false, nil, stagedMatchArtwork{})
}

func (c *Catalog) metadataTarget(kind, id string) (Item, bool) {
	if kind == "film" || kind == "episode" {
		item, ok := c.items[id]
		item.metadataVersion = c.metadataVersions[refreshKey(kind, id)]
		return item, ok && item.Kind == kind
	}
	if kind == "series" {
		series, ok := c.series[id]
		return Item{metadataVersion: c.metadataVersions[refreshKey(kind, id)], ID: series.ID, Title: series.Title, Kind: "series", LocalOnly: series.LocalOnly, ProviderID: series.ProviderID, Provider: series.Provider, Language: series.Language, Region: series.Region, Confidence: series.Confidence, OwnerMatch: series.OwnerMatch, OwnerUnmatch: series.OwnerUnmatch, Year: series.Year, Synopsis: series.Synopsis, Poster: series.Poster, Backdrop: series.Backdrop}, ok
	}
	return Item{}, false
}

func (c *Catalog) saveMatch(kind, id string, enrichment Enrichment, language, region string, owner bool, expected *Item, artwork stagedMatchArtwork) (Item, error) {
	localState, hasLocal, err := c.loadLocalDocument(kind, id)
	if err != nil {
		return Item{}, err
	}
	locked := c.lockedMetadata(kind, id)
	lockedValues := c.protectedMetadataValues(kind, id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return Item{}, ErrMetadataBusy
	}
	current, ok := c.metadataTarget(kind, id)
	if !ok {
		return Item{}, ErrMetadataNotFound
	}
	if expected != nil && !sameMetadataIdentity(current, *expected) {
		return Item{}, ErrMetadataStale
	}
	if kind == "film" || kind == "episode" {
		item, ok := c.items[id]
		if !ok || item.Kind != kind {
			return Item{}, ErrMetadataNotFound
		}
		applyMatch(&item, enrichment, language, region, owner)
		if hasLocal {
			localState.fallback = itemFieldValues(item)
			localState.fallbackProviderID, localState.fallbackProvider = item.ProviderID, item.Provider
			applyFieldValues(&item, localState.fields, locked)
		}
		applyFieldValues(&item, lockedValues, nil)
		item, err := c.updateMatch(kind, item, artwork, localState, hasLocal)
		if err != nil {
			return Item{}, err
		}
		c.advanceMetadataVersion(kind, id)
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
		if hasLocal {
			localState.fallback = itemFieldValues(item)
			localState.fallbackProviderID, localState.fallbackProvider = item.ProviderID, item.Provider
			applyFieldValues(&item, localState.fields, locked)
		}
		applyFieldValues(&item, lockedValues, nil)
		series.ProviderID, series.Provider, series.Language, series.Region, series.Confidence, series.OwnerMatch, series.OwnerUnmatch, series.Year, series.Synopsis, series.Poster, series.Backdrop = item.ProviderID, item.Provider, item.Language, item.Region, item.Confidence, item.OwnerMatch, item.OwnerUnmatch, item.Year, item.Synopsis, item.Poster, item.Backdrop
		series.LocalOnly = !owner
		item, err := c.updateMatch(kind, item, artwork, localState, hasLocal)
		if err != nil {
			return Item{}, err
		}
		series.Poster, series.Backdrop = item.Poster, item.Backdrop
		c.advanceMetadataVersion(kind, id)
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

func (c *Catalog) updateMatch(kind string, item Item, artwork stagedMatchArtwork, localState localDocumentState, hasLocal bool) (Item, error) {
	if c.db == nil {
		return item, nil
	}
	table := "catalog_items"
	if kind == "series" {
		table = "catalog_series"
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
	tx, err := c.db.Begin()
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	if hasLocal {
		fallback, err := json.Marshal(localState.fallback)
		if err != nil {
			return Item{}, err
		}
		if _, err := tx.Exec(`UPDATE catalog_local_metadata SET fallback_json=?,fallback_provider_id=?,fallback_provider=?,updated_at=? WHERE catalog_kind=? AND catalog_id=?`, string(fallback), localState.fallbackProviderID, localState.fallbackProvider, time.Now().Unix(), kind, item.ID); err != nil {
			return Item{}, err
		}
	}
	for _, artworkKind := range []string{"poster", "backdrop"} {
		var localObject, localType, oldFallback string
		localErr := tx.QueryRow(`SELECT l.local_object_name,a.content_type,l.fallback_object_name FROM catalog_local_artwork l JOIN catalog_artwork a ON a.catalog_id=l.catalog_id AND a.kind=l.artwork_kind WHERE l.catalog_kind=? AND l.catalog_id=? AND l.artwork_kind=?`, kind, item.ID, artworkKind).Scan(&localObject, &localType, &oldFallback)
		if localErr != nil && localErr != sql.ErrNoRows {
			return Item{}, localErr
		}
		art, ok := artwork.available[artworkKind]
		if !ok {
			if localErr == nil {
				if _, err := tx.Exec(`UPDATE catalog_local_artwork SET fallback_object_name='',fallback_content_type='',fallback_value='' WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, kind, item.ID, artworkKind); err != nil {
					return Item{}, err
				}
				replaced = append(replaced, oldFallback)
			}
			continue
		}
		name, previous, err := c.replaceArtwork(tx, item.ID, artworkKind, art)
		if err != nil {
			return Item{}, err
		}
		if localErr == nil {
			if _, err := tx.Exec(`UPDATE catalog_artwork SET content_type=?,object_name=? WHERE catalog_id=? AND kind=?`, localType, localObject, item.ID, artworkKind); err != nil {
				return Item{}, err
			}
			if _, err := tx.Exec(`UPDATE catalog_local_artwork SET fallback_object_name=?,fallback_content_type=?,fallback_value=? WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, name, art.ContentType, artworkURL(item.ID, artworkKind), kind, item.ID, artworkKind); err != nil {
				return Item{}, err
			}
			created, replaced = append(created, name), append(replaced, oldFallback)
			continue
		}
		created, replaced = append(created, name), append(replaced, previous)
		if artworkKind == "poster" {
			item.Poster = artworkURL(item.ID, artworkKind)
		} else {
			item.Backdrop = artworkURL(item.ID, artworkKind)
		}
	}
	if _, err := tx.Exec(`UPDATE `+table+` SET provider_id=?,metadata_provider=?,metadata_language=?,metadata_region=?,match_confidence=?,owner_matched=?,owner_unmatched=?,year=?,synopsis=?,poster=?,backdrop=?,local_only=? WHERE id=?`, item.ProviderID, item.Provider, item.Language, item.Region, item.Confidence, boolInt(item.OwnerMatch), boolInt(item.OwnerUnmatch), item.Year, item.Synopsis, item.Poster, item.Backdrop, boolInt(item.LocalOnly), item.ID); err != nil {
		return Item{}, err
	}
	if _, err := tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=?`, kind, item.ID); err != nil {
		return Item{}, err
	}
	if _, err := tx.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind=? AND catalog_id=?`, kind, item.ID); err != nil {
		return Item{}, err
	}
	if kind == "series" {
		if _, err := tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind='episode' AND parent_catalog_id=?`, item.ID); err != nil {
			return Item{}, err
		}
		if _, err := tx.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind='episode' AND parent_catalog_id=?`, item.ID); err != nil {
			return Item{}, err
		}
	}
	for _, artworkKind := range []string{"poster", "backdrop"} {
		providerPath := artwork.pending[artworkKind]
		if providerPath == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO catalog_artwork_retries(catalog_kind,catalog_id,artwork_kind,provider_id,provider_path) VALUES(?,?,?,?,?)`, kind, item.ID, artworkKind, item.ProviderID, providerPath); err != nil {
			return Item{}, err
		}
	}
	if err := detectProviderConflicts(tx); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	committed = true
	return item, nil
}

// Called under mu after a successful owner identity change. Versions only need
// to live for this process because refresh previews are never persisted.
func (c *Catalog) advanceMetadataVersion(kind, id string) {
	if c.metadataVersions == nil {
		c.metadataVersions = make(map[string]uint64)
	}
	c.metadataVersions[refreshKey(kind, id)]++
}
