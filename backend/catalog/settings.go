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
	if failure == "unavailable" {
		return MetadataStatus{Provider: "tmdb", Enabled: true, Configured: true, Source: source, State: "unavailable", Message: "TMDB is unavailable. Cached metadata remains available; retry the scan after service or network recovery."}
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

func (c *Catalog) metadataAccessActive(token string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	current, _ := c.effectiveTMDBTokenLocked()
	return token != "" && current == token
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
	return c.metadataRefreshDueFor(nil, true)
}

func (c *Catalog) metadataRefreshDueFor(requested map[string]bool, honorRetryWait bool) bool {
	c.mu.RLock()
	revision := c.metadataCredentialRevisionLocked()
	db := c.db
	attempted, attemptedAt := c.metadataRefreshAttemptedRevision, c.metadataRefreshAttemptedAt
	c.mu.RUnlock()
	if revision == "" || db == nil {
		return false
	}
	if honorRetryWait && revision == attempted && time.Since(attemptedAt) < metadataRefreshRetryWait {
		return false
	}
	libraries, err := c.configuredLibraryIDs(requested)
	if err != nil || len(libraries) == 0 {
		return err != nil
	}
	for _, libraryID := range libraries {
		var savedRevision, refreshedAt string
		if err := db.QueryRow("SELECT value FROM settings WHERE key=?", metadataRevisionKey(libraryID)).Scan(&savedRevision); err != nil && err != sql.ErrNoRows {
			return true
		}
		if savedRevision != revision {
			return true
		}
		if err := db.QueryRow("SELECT value FROM settings WHERE key=?", metadataRefreshedAtKey(libraryID)).Scan(&refreshedAt); err != nil {
			return true
		}
		unix, err := strconv.ParseInt(refreshedAt, 10, 64)
		if err != nil || time.Since(time.Unix(unix, 0)) >= metadataRefreshMaxAge {
			return true
		}
	}
	return false
}

func metadataRevisionKey(libraryID string) string    { return "metadata_credential_revision:" + libraryID }
func metadataRefreshedAtKey(libraryID string) string { return "metadata_refreshed_at:" + libraryID }

func (c *Catalog) configuredLibraryIDs(requested map[string]bool) ([]string, error) {
	rows, err := c.db.Query(`SELECT DISTINCT l.id FROM libraries l JOIN library_locations x ON x.library_id=l.id WHERE x.root_path<>'' ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if len(requested) == 0 || requested[id] {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func (c *Catalog) recordMetadataRefresh(revision string, requested map[string]bool, scanID string) {
	if c.db == nil {
		return
	}
	libraries, err := c.configuredLibraryIDs(requested)
	if err != nil {
		return
	}
	tx, err := c.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	refreshedAt := strconv.FormatInt(time.Now().Unix(), 10)
	for _, libraryID := range libraries {
		var total, completed int
		if err := tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN last_scan_id=? THEN 1 ELSE 0 END),0) FROM library_locations WHERE library_id=? AND root_path<>''`, scanID, libraryID).Scan(&total, &completed); err != nil || total == 0 || completed != total {
			continue
		}
		for key, value := range map[string]string{metadataRevisionKey(libraryID): revision, metadataRefreshedAtKey(libraryID): refreshedAt} {
			if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return
	}
	if c.metadataRefreshDueFor(nil, false) {
		return
	}
	tx, err = c.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for key, value := range map[string]string{"metadata_credential_revision": revision, "metadata_refreshed_at": refreshedAt} {
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
