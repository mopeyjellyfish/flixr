package catalog

import (
	"context"
	"strings"
)

type MetadataStatus struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
	State      string `json:"state"`
	Message    string `json:"message"`
}

func (c *Catalog) ValidateTMDBToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidCredential
	}
	c.mu.RLock()
	validator, ok := c.provider.(MetadataValidator)
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	return validator.Validate(ctx, token)
}

func (c *Catalog) MetadataStatus() MetadataStatus {
	c.mu.RLock()
	configured, scan := c.token != "", c.status
	c.mu.RUnlock()
	if !configured {
		return MetadataStatus{Provider: "tmdb", State: "unavailable", Message: "Add a TMDB API Read Access Token to enable artwork and descriptions."}
	}
	if scan.Status == "running" {
		return MetadataStatus{Provider: "tmdb", Configured: true, State: "running", Message: "Metadata enrichment is running with the library scan."}
	}
	if c.db != nil && scan.ID != "" {
		var failures int
		_ = c.db.QueryRow(`SELECT COUNT(*) FROM scan_files WHERE scan_id=? AND outcome IN ('provider_failed','provider_artwork_failed')`, scan.ID).Scan(&failures)
		if failures > 0 {
			return MetadataStatus{Provider: "tmdb", Configured: true, State: "failed", Message: "Metadata failed for some titles. Check the credential or connection, then start another scan."}
		}
	}
	return MetadataStatus{Provider: "tmdb", Configured: true, State: "configured", Message: "TMDB is configured. Start a scan to enrich new or unchanged files."}
}

// SetTMDBToken persists the owner credential but never exposes it through catalog views.
func (c *Catalog) SetTMDBToken(token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
	if c.db == nil {
		return nil
	}
	if token == "" {
		_, err := c.db.Exec("DELETE FROM settings WHERE key='tmdb_token'")
		return err
	}
	_, err := c.db.Exec("INSERT INTO settings(key,value) VALUES('tmdb_token',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", token)
	return err
}
func (c *Catalog) TMDBConfigured() bool { c.mu.RLock(); defer c.mu.RUnlock(); return c.token != "" }

// SetProvider makes the scan-time provider replaceable at its narrow consumer-owned seam.
func (c *Catalog) SetProvider(provider MetadataProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = provider
}
