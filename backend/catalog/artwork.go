package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func artworkURL(id, kind string) string {
	return "/api/v1/catalog/artwork/" + id + "/" + kind
}
func artworkFile(dir, id, kind string) string {
	return filepath.Join(dir, "artwork", id+"-"+kind)
}
func allowedArtworkContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

func (c *Catalog) cacheArtwork(id, kind string, art Artwork) (string, error) {
	if c.db == nil || (kind != "poster" && kind != "backdrop") || len(art.Bytes) == 0 || !allowedArtworkContentType(art.ContentType) {
		return "", nil
	}
	dir := filepath.Join(c.db.DataDir(), "artwork")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(art.Bytes); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(name, artworkFile(c.db.DataDir(), id, kind)); err != nil {
		return "", err
	}
	if _, err := c.db.Exec(`INSERT INTO catalog_artwork(catalog_id,kind,content_type) VALUES(?,?,?) ON CONFLICT(catalog_id,kind) DO UPDATE SET content_type=excluded.content_type`, id, kind, art.ContentType); err != nil {
		return "", err
	}
	return artworkURL(id, kind), nil
}

func (c *Catalog) cacheEnrichmentArtwork(ctx context.Context, id string, enrichment *Enrichment) bool {
	c.mu.RLock()
	provider, ok := c.provider.(ArtworkProvider)
	c.mu.RUnlock()
	if !ok || c.db == nil {
		enrichment.Poster, enrichment.Backdrop = "", ""
		return false
	}
	failed := false
	if enrichment.Poster != "" {
		if art, err := provider.FetchArtwork(ctx, enrichment.Poster); err == nil {
			if cached, err := c.cacheArtwork(id, "poster", art); err == nil && cached != "" {
				enrichment.Poster = cached
			} else {
				enrichment.Poster, failed = "", true
			}
		} else {
			enrichment.Poster, failed = "", true
		}
	}
	if enrichment.Backdrop != "" {
		if art, err := provider.FetchArtwork(ctx, enrichment.Backdrop); err == nil {
			if cached, err := c.cacheArtwork(id, "backdrop", art); err == nil && cached != "" {
				enrichment.Backdrop = cached
			} else {
				enrichment.Backdrop, failed = "", true
			}
		} else {
			enrichment.Backdrop, failed = "", true
		}
	}
	return failed
}

// Artwork returns only a catalog-ID-derived cached asset; provider and filesystem paths are never accepted.
func (c *Catalog) Artwork(id, kind string) ([]byte, string, error) {
	if c.db == nil || (kind != "poster" && kind != "backdrop") {
		return nil, "", os.ErrNotExist
	}
	var contentType string
	if err := c.db.QueryRow(`SELECT content_type FROM catalog_artwork WHERE catalog_id=? AND kind=?`, id, kind).Scan(&contentType); err != nil {
		return nil, "", os.ErrNotExist
	}
	data, err := os.ReadFile(artworkFile(c.db.DataDir(), id, kind))
	if err != nil {
		return nil, "", fmt.Errorf("read cached artwork: %w", err)
	}
	return data, contentType, nil
}
