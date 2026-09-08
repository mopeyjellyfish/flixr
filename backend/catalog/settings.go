package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	metadataRefreshMaxAge    = 150 * 24 * time.Hour
	metadataRefreshRetryWait = time.Minute
)

type MetadataStatus struct {
	Provider   string `json:"provider"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
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
	token, source := c.effectiveTMDBTokenLocked()
	enabled, scan, failure := c.metadataEnabled, c.status, c.metadataFailure
	c.mu.RUnlock()
	if !enabled {
		return MetadataStatus{Provider: "tmdb", Source: "disabled", State: "disabled", Message: "Remote metadata is disabled. Cached artwork and descriptions remain available."}
	}
	if token == "" {
		return MetadataStatus{Provider: "tmdb", Enabled: true, Source: "none", State: "unavailable", Message: "This build has no automatic metadata access. A personal TMDB token can be added in Advanced settings."}
	}
	if scan.Status == "running" {
		return MetadataStatus{Provider: "tmdb", Enabled: true, Configured: true, Source: source, State: "running", Message: "Metadata enrichment is running with the library scan."}
	}
	if failure == "rate_limited" {
		return MetadataStatus{Provider: "tmdb", Enabled: true, Configured: true, Source: source, State: "rate_limited", Message: "TMDB's request limit was reached. Cached metadata remains available; retry the scan later."}
	}
	if failure == "invalid_credential" {
		return MetadataStatus{Provider: "tmdb", Enabled: true, Configured: true, Source: source, State: "failed", Message: "TMDB rejected the configured access. Cached metadata remains available; replace the override or update the application build."}
	}
	if c.db != nil && scan.ID != "" {
		var failures int
		_ = c.db.QueryRow(`SELECT COUNT(*) FROM scan_files WHERE scan_id=? AND outcome IN ('provider_failed','provider_artwork_failed')`, scan.ID).Scan(&failures)
		if failures > 0 {
			return MetadataStatus{Provider: "tmdb", Enabled: true, Configured: true, Source: source, State: "failed", Message: "Metadata failed for some titles. Check provider access or the connection, then start another scan."}
		}
	}
	message := "Automatic TMDB access is available. Start a scan to enrich new or unchanged files."
	if source == "owner" {
		message = "A personal TMDB override is configured. Start a scan to enrich new or unchanged files."
	}
	return MetadataStatus{Provider: "tmdb", Enabled: true, Configured: true, Source: source, State: "configured", Message: message}
}

func (c *Catalog) noteMetadataFailure(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if errors.Is(err, ErrProviderRateLimited) {
		c.metadataFailure = "rate_limited"
	} else if errors.Is(err, ErrInvalidCredential) && c.metadataFailure == "" {
		c.metadataFailure = "invalid_credential"
	} else if c.metadataFailure == "" {
		c.metadataFailure = "unavailable"
	}
}

func (c *Catalog) effectiveTMDBTokenLocked() (string, string) {
	if !c.metadataEnabled {
		return "", "disabled"
	}
	if c.token != "" {
		return c.token, "owner"
	}
	if c.applicationToken != "" {
		return c.applicationToken, "application"
	}
	return "", "none"
}

func (c *Catalog) metadataCredentialRevisionLocked() string {
	token, _ := c.effectiveTMDBTokenLocked()
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MetadataRefreshDue reports whether an existing configured library needs one
// bounded provider scan for a new credential or cache-renewal interval.
func (c *Catalog) MetadataRefreshDue() bool {
	c.mu.RLock()
	revision := c.metadataCredentialRevisionLocked()
	hasRoot := c.film != "" || c.tv != ""
	db := c.db
	attempted, attemptedAt := c.metadataRefreshAttemptedRevision, c.metadataRefreshAttemptedAt
	c.mu.RUnlock()
	if revision == "" || !hasRoot || db == nil {
		return false
	}
	if revision == attempted && time.Since(attemptedAt) < metadataRefreshRetryWait {
		return false
	}
	var savedRevision, refreshedAt string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='metadata_credential_revision'").Scan(&savedRevision); err != nil && err != sql.ErrNoRows {
		return true
	}
	if savedRevision != revision {
		return true
	}
	if err := db.QueryRow("SELECT value FROM settings WHERE key='metadata_refreshed_at'").Scan(&refreshedAt); err != nil {
		return true
	}
	unix, err := strconv.ParseInt(refreshedAt, 10, 64)
	return err != nil || time.Since(time.Unix(unix, 0)) >= metadataRefreshMaxAge
}

func (c *Catalog) recordMetadataRefresh(revision string) {
	if c.db == nil {
		return
	}
	tx, err := c.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		"metadata_credential_revision": revision,
		"metadata_refreshed_at":        strconv.FormatInt(time.Now().Unix(), 10),
	} {
		if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

// SetApplicationTMDBToken supplies the distributable application credential.
// It is process-only and is never written to the household database.
func (c *Catalog) SetApplicationTMDBToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applicationToken = strings.TrimSpace(token)
}

// SetMetadataEnabled persists remote lookup policy independently from credentials.
func (c *Catalog) SetMetadataEnabled(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		value := "false"
		if enabled {
			value = "true"
		}
		if _, err := c.db.Exec("INSERT INTO settings(key,value) VALUES('metadata_enabled',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value); err != nil {
			return err
		}
	}
	c.metadataEnabled = enabled
	return nil
}

func (c *Catalog) MetadataEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.metadataEnabled
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
func (c *Catalog) TMDBConfigured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	token, _ := c.effectiveTMDBTokenLocked()
	return token != ""
}

func (c *Catalog) TMDBOverrideConfigured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token != ""
}

// SetProvider makes the scan-time provider replaceable at its narrow consumer-owned seam.
func (c *Catalog) SetProvider(provider MetadataProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = provider
}
