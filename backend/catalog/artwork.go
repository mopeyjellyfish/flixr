package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/afero"
	"golang.org/x/image/draw"
)

const (
	maxArtworkWidth     = 1600
	maxArtworkHeight    = 2400
	maxArtworkPixels    = 12_000_000
	maxArtworkSource    = 5 << 20
	maxDerivativeBytes  = 128 << 20
	derivativeMaxAge    = 7 * 24 * time.Hour
	maintenanceMaxFiles = 64
	maintenanceBatch    = 256
)

var artworkWork = make(chan struct{}, 2)

type sizedArtwork struct {
	data        []byte
	contentType string
}

type artworkIdentity struct {
	catalogKind      string
	catalogID        string
	providerID       string
	parentCatalogID  string
	parentProviderID string
}

func (c *Catalog) startArtworkMaintenance() {
	ctx, cancel := context.WithCancel(context.Background())
	c.maintenanceCancel, c.maintenanceDone = cancel, make(chan struct{})
	go func() {
		defer close(c.maintenanceDone)
		defer func() {
			c.artworkMu.Lock()
			defer c.artworkMu.Unlock()
			for _, directory := range []afero.File{c.maintenanceDir, c.artworkObjectsDir} {
				if directory != nil {
					_ = directory.Close()
				}
			}
			c.maintenanceDir, c.artworkObjectsDir = nil, nil
		}()
		run := func() {
			c.artworkMu.Lock()
			err := c.cleanupDerivativesLocked(time.Now())
			if objectErr := c.cleanupArtworkObjectsLocked(); err == nil {
				err = objectErr
			}
			outcome := "complete"
			if err != nil {
				outcome = "failed"
			}
			c.maintenanceStatus = ArtworkMaintenanceStatus{LastRun: time.Now(), Outcome: outcome}
			c.artworkMu.Unlock()
		}
		run()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func artworkURL(id, kind string) string {
	return "/api/v1/catalog/artwork/" + id + "/" + kind
}
func artworkFile(dir, id, kind string) string {
	return filepath.Join(dir, "artwork", id+"-"+kind)
}
func derivativeFile(dir, id, kind, revision string, width int) string {
	return filepath.Join(dir, "artwork", "derivatives", id+"-"+kind+"-"+revision+"-"+strconv.Itoa(width)+".jpg")
}

func derivativeWidth(width int) int {
	for _, size := range [...]int{160, 240, 320, 480, 640, 960, 1280, maxArtworkWidth} {
		if width <= size {
			return size
		}
	}
	return maxArtworkWidth
}
func allowedArtworkContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

// Artwork objects are immutable. Only the database reference publishes them.
// Callers hold artworkMu until commit/rollback and cleanup, so readers cannot
// race replacement cleanup and maintenance cannot remove unpublished objects.
func (c *Catalog) replaceArtwork(tx *sql.Tx, id, kind string, art Artwork) (string, string, error) {
	if (kind != "poster" && kind != "backdrop") || len(art.Bytes) == 0 || len(art.Bytes) > maxArtworkSource || !allowedArtworkContentType(art.ContentType) {
		return "", "", errors.New("invalid artwork")
	}
	var previous string
	if err := tx.QueryRow(`SELECT object_name FROM catalog_artwork WHERE catalog_id=? AND kind=?`, id, kind).Scan(&previous); err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	dir := filepath.Join(c.db.DataDir(), "artwork", "objects")
	if err := c.fs.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	file, err := afero.TempFile(c.fs, dir, "image-")
	if err != nil {
		return "", "", err
	}
	name := filepath.Base(file.Name())
	retained := false
	defer func() {
		if !retained {
			_ = file.Close()
			_ = c.fs.Remove(file.Name())
		}
	}()
	if err := c.fs.Chmod(file.Name(), 0o600); err != nil {
		return "", "", err
	}
	if _, err := file.Write(art.Bytes); err != nil {
		return "", "", err
	}
	if err := file.Sync(); err != nil {
		return "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(`INSERT INTO catalog_artwork(catalog_id,kind,content_type,object_name) VALUES(?,?,?,?) ON CONFLICT(catalog_id,kind) DO UPDATE SET content_type=excluded.content_type,object_name=excluded.object_name`, id, kind, art.ContentType, name); err != nil {
		return "", "", err
	}
	retained = true
	return name, previous, nil
}

func (c *Catalog) removeArtworkObject(name string) {
	if name != "" && filepath.Base(name) == name {
		_ = c.fs.Remove(filepath.Join(c.db.DataDir(), "artwork", "objects", name))
	}
}

func (c *Catalog) cacheArtwork(id, kind string, art Artwork) (string, error) {
	if c.db == nil {
		return "", nil
	}
	c.artworkMu.Lock()
	defer c.artworkMu.Unlock()
	tx, err := c.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	name, previous, err := c.replaceArtwork(tx, id, kind, art)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		c.removeArtworkObject(name)
		return "", err
	}
	c.removeArtworkObject(previous)
	return artworkURL(id, kind), nil
}

func (c *Catalog) cacheArtworkAndClearRetry(identity artworkIdentity, kind string, art Artwork) (cached string, err error) {
	if c.db == nil {
		return "", nil
	}
	completedRetry := false
	cached, err = func() (string, error) {
		c.artworkMu.Lock()
		defer c.artworkMu.Unlock()
		tx, err := c.db.Begin()
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		var pending int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=? AND provider_id=? AND parent_catalog_id=? AND parent_provider_id=?)`, identity.catalogKind, identity.catalogID, kind, identity.providerID, identity.parentCatalogID, identity.parentProviderID).Scan(&pending); err != nil {
			return "", err
		}
		var localObject, localType, oldFallback string
		localErr := tx.QueryRow(`SELECT l.local_object_name,a.content_type,l.fallback_object_name FROM catalog_local_artwork l JOIN catalog_artwork a ON a.catalog_id=l.catalog_id AND a.kind=l.artwork_kind WHERE l.catalog_kind=? AND l.catalog_id=? AND l.artwork_kind=?`, identity.catalogKind, identity.catalogID, kind).Scan(&localObject, &localType, &oldFallback)
		if localErr != nil && localErr != sql.ErrNoRows {
			return "", localErr
		}
		if localErr == nil {
			name, _, err := c.replaceArtwork(tx, identity.catalogID, kind, art)
			if err != nil {
				return "", err
			}
			if _, err := tx.Exec(`UPDATE catalog_artwork SET content_type=?,object_name=? WHERE catalog_id=? AND kind=?`, localType, localObject, identity.catalogID, kind); err != nil {
				c.removeArtworkObject(name)
				return "", err
			}
			if _, err := tx.Exec(`UPDATE catalog_local_artwork SET fallback_object_name=?,fallback_content_type=?,fallback_value=? WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=?`, name, art.ContentType, artworkURL(identity.catalogID, kind), identity.catalogKind, identity.catalogID, kind); err != nil {
				c.removeArtworkObject(name)
				return "", err
			}
			if pending != 0 {
				if _, err := tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=? AND provider_id=? AND parent_catalog_id=? AND parent_provider_id=?`, identity.catalogKind, identity.catalogID, kind, identity.providerID, identity.parentCatalogID, identity.parentProviderID); err != nil {
					c.removeArtworkObject(name)
					return "", err
				}
			}
			if err := tx.Commit(); err != nil {
				c.removeArtworkObject(name)
				return "", err
			}
			c.removeArtworkObject(oldFallback)
			return artworkURL(identity.catalogID, kind), nil
		}
		name, previous, err := c.replaceArtwork(tx, identity.catalogID, kind, art)
		if err != nil {
			return "", err
		}
		if pending != 0 {
			url := artworkURL(identity.catalogID, kind)
			result, err := c.updateArtworkReference(tx, identity, kind, url)
			if err != nil {
				c.removeArtworkObject(name)
				return "", err
			}
			if changed, err := result.RowsAffected(); err != nil || changed != 1 {
				c.removeArtworkObject(name)
				if err != nil {
					return "", err
				}
				return "", errors.New("artwork retry identity is stale")
			}
			if _, err := tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=? AND artwork_kind=? AND provider_id=? AND parent_catalog_id=? AND parent_provider_id=?`, identity.catalogKind, identity.catalogID, kind, identity.providerID, identity.parentCatalogID, identity.parentProviderID); err != nil {
				c.removeArtworkObject(name)
				return "", err
			}
		}
		if err := tx.Commit(); err != nil {
			c.removeArtworkObject(name)
			return "", err
		}
		c.removeArtworkObject(previous)
		completedRetry = pending != 0
		return artworkURL(identity.catalogID, kind), nil
	}()
	if err == nil && completedRetry {
		c.syncArtworkReference(identity, kind, cached)
	}
	return cached, err
}

func (c *Catalog) updateArtworkReference(tx *sql.Tx, identity artworkIdentity, kind, value string) (sql.Result, error) {
	column := "poster"
	if kind == "backdrop" {
		column = "backdrop"
	}
	switch identity.catalogKind {
	case "film":
		return tx.Exec(`UPDATE catalog_items SET `+column+`=? WHERE id=? AND kind='film' AND provider_id=?`, value, identity.catalogID, identity.providerID)
	case "series":
		return tx.Exec(`UPDATE catalog_series SET `+column+`=? WHERE id=? AND provider_id=?`, value, identity.catalogID, identity.providerID)
	case "episode":
		return tx.Exec(`UPDATE catalog_items SET `+column+`=? WHERE id=? AND kind='episode' AND provider_id=? AND series_id=? AND EXISTS (SELECT 1 FROM catalog_series WHERE id=? AND provider_id=?)`, value, identity.catalogID, identity.providerID, identity.parentCatalogID, identity.parentCatalogID, identity.parentProviderID)
	default:
		return nil, errors.New("invalid artwork catalog kind")
	}
}

func (c *Catalog) syncArtworkReference(identity artworkIdentity, kind, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if identity.catalogKind == "series" {
		series, ok := c.series[identity.catalogID]
		if !ok || series.ProviderID != identity.providerID {
			return
		}
		if kind == "poster" {
			series.Poster = value
		} else {
			series.Backdrop = value
		}
		c.series[identity.catalogID] = series
		return
	}
	item, ok := c.items[identity.catalogID]
	if !ok || item.ProviderID != identity.providerID {
		return
	}
	if identity.catalogKind == "episode" {
		parent, ok := c.series[identity.parentCatalogID]
		if !ok || item.SeriesID != identity.parentCatalogID || parent.ProviderID != identity.parentProviderID {
			return
		}
	}
	if kind == "poster" {
		item.Poster = value
	} else {
		item.Backdrop = value
	}
	c.items[identity.catalogID] = item
}

func (c *Catalog) rememberArtworkRetry(identity artworkIdentity, kind, providerPath string) error {
	if c.db == nil {
		return nil
	}
	_, err := c.db.Exec(`INSERT INTO catalog_artwork_retries(catalog_kind,catalog_id,artwork_kind,provider_id,parent_catalog_id,parent_provider_id,provider_path) VALUES(?,?,?,?,?,?,?) ON CONFLICT(catalog_kind,catalog_id,artwork_kind) DO UPDATE SET provider_id=excluded.provider_id,parent_catalog_id=excluded.parent_catalog_id,parent_provider_id=excluded.parent_provider_id,provider_path=excluded.provider_path`, identity.catalogKind, identity.catalogID, kind, identity.providerID, identity.parentCatalogID, identity.parentProviderID, providerPath)
	return err
}

func (c *Catalog) pendingArtwork(identity artworkIdentity) (Enrichment, error) {
	enrichment := Enrichment{ProviderID: identity.providerID}
	if c.db == nil || identity.providerID == "" {
		return enrichment, nil
	}
	rows, err := c.db.Query(`SELECT artwork_kind,provider_path FROM catalog_artwork_retries WHERE catalog_kind=? AND catalog_id=? AND provider_id=? AND parent_catalog_id=? AND parent_provider_id=?`, identity.catalogKind, identity.catalogID, identity.providerID, identity.parentCatalogID, identity.parentProviderID)
	if err != nil {
		return Enrichment{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, providerPath string
		if err := rows.Scan(&kind, &providerPath); err != nil {
			return Enrichment{}, err
		}
		if kind == "poster" {
			enrichment.Poster = providerPath
		} else if kind == "backdrop" {
			enrichment.Backdrop = providerPath
		}
	}
	return enrichment, rows.Err()
}

func (c *Catalog) pendingLegacyArtworkReconciliation(identity artworkIdentity) (bool, error) {
	if c.db == nil || identity.providerID == "" {
		return false, nil
	}
	var pending int
	err := c.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_artwork_reconciliations WHERE catalog_kind=? AND catalog_id=? AND provider_id=? AND parent_catalog_id=? AND parent_provider_id=?)`, identity.catalogKind, identity.catalogID, identity.providerID, identity.parentCatalogID, identity.parentProviderID).Scan(&pending)
	return pending != 0, err
}

func (c *Catalog) completeLegacyArtworkReconciliation(identity artworkIdentity) error {
	if c.db == nil {
		return nil
	}
	_, err := c.db.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind=? AND catalog_id=? AND provider_id=? AND parent_catalog_id=? AND parent_provider_id=?`, identity.catalogKind, identity.catalogID, identity.providerID, identity.parentCatalogID, identity.parentProviderID)
	return err
}

func (c *Catalog) stageLegacyArtworkRetries(identity artworkIdentity, enrichment Enrichment) error {
	if c.db == nil {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, value := range []struct {
		kind, path string
	}{{"poster", enrichment.Poster}, {"backdrop", enrichment.Backdrop}} {
		if value.path == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO catalog_artwork_retries(catalog_kind,catalog_id,artwork_kind,provider_id,parent_catalog_id,parent_provider_id,provider_path) VALUES(?,?,?,?,?,?,?) ON CONFLICT(catalog_kind,catalog_id,artwork_kind) DO UPDATE SET provider_id=excluded.provider_id,parent_catalog_id=excluded.parent_catalog_id,parent_provider_id=excluded.parent_provider_id,provider_path=excluded.provider_path`, identity.catalogKind, identity.catalogID, value.kind, identity.providerID, identity.parentCatalogID, identity.parentProviderID, value.path); err != nil {
			return err
		}
	}
	result, err := tx.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind=? AND catalog_id=? AND provider_id=? AND parent_catalog_id=? AND parent_provider_id=?`, identity.catalogKind, identity.catalogID, identity.providerID, identity.parentCatalogID, identity.parentProviderID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return err
		}
		return errors.New("legacy artwork reconciliation identity is stale")
	}
	return tx.Commit()
}

func missingArtwork(enrichment Enrichment, existingPoster, existingBackdrop string) Enrichment {
	if existingPoster != "" {
		enrichment.Poster = ""
	}
	if existingBackdrop != "" {
		enrichment.Backdrop = ""
	}
	return enrichment
}

func (c *Catalog) unlockedArtwork(kind, id string, enrichment Enrichment) Enrichment {
	fields, err := c.MetadataFields(kind, id)
	if err != nil {
		return enrichment
	}
	for _, field := range fields {
		if !field.Locked && field.Source != "local" {
			continue
		}
		switch field.Field {
		case "poster":
			enrichment.Poster = ""
		case "backdrop":
			enrichment.Backdrop = ""
		}
	}
	return enrichment
}

func (c *Catalog) cacheEnrichmentArtwork(ctx context.Context, identity artworkIdentity, enrichment *Enrichment, existingPoster, existingBackdrop string) (bool, error) {
	posterPath, backdropPath := enrichment.Poster, enrichment.Backdrop
	c.mu.RLock()
	provider, ok := c.provider.(ArtworkProvider)
	c.mu.RUnlock()
	if !ok || c.db == nil {
		enrichment.Poster, enrichment.Backdrop = existingPoster, existingBackdrop
		return (existingPoster == "" && posterPath != "") || (existingBackdrop == "" && backdropPath != ""), nil
	}
	enrichment.Poster, enrichment.Backdrop = existingPoster, existingBackdrop
	failed := false
	if enrichment.Poster == "" && posterPath != "" {
		if !c.MetadataEnabled() {
			return false, nil
		}
		if art, err := provider.FetchArtwork(ctx, posterPath); err == nil {
			if cached, err := c.cacheArtworkAndClearRetry(identity, "poster", art); err == nil && cached != "" {
				enrichment.Poster = cached
			} else {
				enrichment.Poster, failed = "", true
			}
		} else {
			enrichment.Poster, failed = "", true
		}
		if enrichment.Poster == "" {
			if err := c.rememberArtworkRetry(identity, "poster", posterPath); err != nil {
				return true, fmt.Errorf("remember poster artwork retry: %w", err)
			}
		}
	}
	if enrichment.Backdrop == "" && backdropPath != "" {
		if !c.MetadataEnabled() {
			return failed, nil
		}
		if art, err := provider.FetchArtwork(ctx, backdropPath); err == nil {
			if cached, err := c.cacheArtworkAndClearRetry(identity, "backdrop", art); err == nil && cached != "" {
				enrichment.Backdrop = cached
			} else {
				enrichment.Backdrop, failed = "", true
			}
		} else {
			enrichment.Backdrop, failed = "", true
		}
		if enrichment.Backdrop == "" {
			if err := c.rememberArtworkRetry(identity, "backdrop", backdropPath); err != nil {
				return true, fmt.Errorf("remember backdrop artwork retry: %w", err)
			}
		}
	}
	return failed, nil
}

// Artwork returns only a catalog-ID-derived cached asset; provider and filesystem paths are never accepted.
func (c *Catalog) Artwork(id, kind string) ([]byte, string, error) {
	if c.db == nil || (kind != "poster" && kind != "backdrop") {
		return nil, "", os.ErrNotExist
	}
	c.artworkMu.Lock()
	defer c.artworkMu.Unlock()
	var contentType, objectName string
	if err := c.db.QueryRow(`SELECT content_type,object_name FROM catalog_artwork WHERE catalog_id=? AND kind=?`, id, kind).Scan(&contentType, &objectName); err != nil {
		return nil, "", os.ErrNotExist
	}
	path := artworkFile(c.db.DataDir(), id, kind)
	if objectName != "" {
		if filepath.Base(objectName) != objectName {
			return nil, "", os.ErrNotExist
		}
		path = filepath.Join(c.db.DataDir(), "artwork", "objects", objectName)
	}
	data, err := afero.ReadFile(c.fs, path)
	if err != nil {
		return nil, "", fmt.Errorf("read cached artwork: %w", err)
	}
	return data, contentType, nil
}

// ArtworkSized returns a bounded JPEG derivative where possible. Originals remain
// durable; any derivative failure falls back to the original cached artwork.
func (c *Catalog) ArtworkSized(ctx context.Context, id, kind string, width int) ([]byte, string, error) {
	original, contentType, err := c.Artwork(id, kind)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return original, contentType, cancelErr
	}
	if err != nil || width <= 0 {
		return original, contentType, err
	}
	width = derivativeWidth(width)
	revision := fmt.Sprintf("%x", sha256.Sum256(original))[:16]
	key := id + ":" + kind + ":" + revision + ":" + strconv.Itoa(width)
	result := c.artworkGroup.DoChan(key, func() (any, error) {
		c.artworkMu.Lock()
		path := derivativeFile(c.db.DataDir(), id, kind, revision, width)
		if data, readErr := afero.ReadFile(c.fs, path); readErr == nil {
			_ = c.fs.Chtimes(path, time.Now(), time.Now())
			c.artworkMu.Unlock()
			return sizedArtwork{data, "image/jpeg"}, nil
		}
		c.artworkMu.Unlock()
		select {
		case artworkWork <- struct{}{}:
			defer func() { <-artworkWork }()
		case <-time.After(100 * time.Millisecond):
			return nil, errors.New("artwork resize busy")
		}
		if len(original) > maxArtworkSource {
			return nil, errors.New("cached artwork exceeds resize limit")
		}
		config, _, configErr := image.DecodeConfig(bytes.NewReader(original))
		if configErr != nil || config.Width < 1 || config.Height < 1 || config.Width > maxArtworkPixels/config.Height {
			return nil, errors.New("cached artwork dimensions are unsafe")
		}
		height := max(1, config.Height*width/config.Width)
		if height > maxArtworkHeight {
			return nil, errors.New("artwork derivative dimensions are unsafe")
		}
		decoded, _, decodeErr := image.Decode(bytes.NewReader(original))
		if decodeErr != nil {
			return nil, fmt.Errorf("decode cached artwork: %w", decodeErr)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() <= width {
			return sizedArtwork{original, contentType}, nil
		}
		resized := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(resized, resized.Bounds(), decoded, bounds, draw.Over, nil)
		var output bytes.Buffer
		if encodeErr := jpeg.Encode(&output, resized, &jpeg.Options{Quality: 82}); encodeErr != nil {
			return nil, fmt.Errorf("encode artwork derivative: %w", encodeErr)
		}
		c.artworkMu.Lock()
		var writeErr error
		if c.derivativeReady && c.derivativeCount < maintenanceMaxFiles && c.derivativeBytes+int64(output.Len()) <= maxDerivativeBytes {
			writeErr = c.writeDerivative(path, output.Bytes())
			if writeErr == nil {
				c.derivativeBytes += int64(output.Len())
				c.derivativeCount++
			}
		}
		c.artworkMu.Unlock()
		if writeErr != nil {
			return nil, writeErr
		}
		return sizedArtwork{output.Bytes(), "image/jpeg"}, nil
	})
	select {
	case <-ctx.Done():
		return original, contentType, ctx.Err()
	case completed := <-result:
		if err := ctx.Err(); err != nil {
			return original, contentType, err
		}
		if completed.Err != nil {
			return original, contentType, nil
		}
		sized := completed.Val.(sizedArtwork)
		return sized.data, sized.contentType, nil
	}
}

func (c *Catalog) writeDerivative(path string, data []byte) error {
	if err := c.fs.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := afero.TempFile(c.fs, filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer c.fs.Remove(name)
	if err := c.fs.Chmod(name, 0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return c.fs.Rename(name, path)
}

func (c *Catalog) cleanupDerivativesLocked(now time.Time) error {
	dir := filepath.Join(c.db.DataDir(), "artwork", "derivatives")
	if c.maintenanceDir == nil {
		directory, err := c.fs.Open(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				c.derivativeReady = true
				return nil
			}
			return err
		}
		c.maintenanceDir, c.derivativeBytes, c.derivativeCount, c.derivativeReady = directory, 0, 0, false
	}
	entries, err := c.maintenanceDir.Readdir(maintenanceBatch)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if strings.HasPrefix(entry.Name(), ".tmp-") && now.Sub(entry.ModTime()) > time.Hour {
			if err := c.fs.Remove(path); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jpg") {
			continue
		}
		if now.Sub(entry.ModTime()) > derivativeMaxAge || c.derivativeCount >= maintenanceMaxFiles || c.derivativeBytes+entry.Size() > maxDerivativeBytes {
			c.derivativeBytes += entry.Size()
			c.derivativeCount++
			if err := c.fs.Remove(path); err != nil {
				return err
			}
			c.derivativeBytes -= entry.Size()
			c.derivativeCount--
			continue
		}
		c.derivativeBytes += entry.Size()
		c.derivativeCount++
	}
	if errors.Is(err, io.EOF) {
		_ = c.maintenanceDir.Close()
		c.maintenanceDir = nil
		c.derivativeReady = true
	}
	return nil
}

// Incremental orphan collection recovers objects left by a crash before commit.
// The shared artwork lock excludes active writers and readers during collection.
func (c *Catalog) cleanupArtworkObjectsLocked() error {
	dir := filepath.Join(c.db.DataDir(), "artwork", "objects")
	if c.artworkObjectsDir == nil {
		directory, err := c.fs.Open(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		c.artworkObjectsDir = directory
	}
	entries, err := c.artworkObjectsDir.Readdir(maintenanceBatch)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "image-") {
			continue
		}
		var count int
		if err := c.db.QueryRow(`SELECT (SELECT count(*) FROM catalog_artwork WHERE object_name=?)+(SELECT count(*) FROM catalog_local_artwork WHERE local_object_name=? OR fallback_object_name=?)+(SELECT count(*) FROM catalog_local_identity_artwork WHERE object_name=?)`, entry.Name(), entry.Name(), entry.Name(), entry.Name()).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if err := c.fs.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if errors.Is(err, io.EOF) {
		_ = c.artworkObjectsDir.Close()
		c.artworkObjectsDir = nil
	}
	return nil
}
