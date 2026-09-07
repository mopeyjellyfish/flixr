package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
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

func (c *Catalog) startArtworkMaintenance() {
	ctx, cancel := context.WithCancel(context.Background())
	c.maintenanceCancel, c.maintenanceDone = cancel, make(chan struct{})
	go func() {
		defer close(c.maintenanceDone)
		run := func() {
			c.artworkMu.Lock()
			err := c.cleanupDerivativesLocked(time.Now())
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

func (c *Catalog) cacheArtwork(id, kind string, art Artwork) (string, error) {
	if c.db == nil || (kind != "poster" && kind != "backdrop") || len(art.Bytes) == 0 || !allowedArtworkContentType(art.ContentType) {
		return "", nil
	}
	dir := filepath.Join(c.db.DataDir(), "artwork")
	if err := c.fs.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := afero.TempFile(c.fs, dir, ".tmp-")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer c.fs.Remove(name)
	if err := c.fs.Chmod(name, 0o600); err != nil {
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
	if err := c.fs.Rename(name, artworkFile(c.db.DataDir(), id, kind)); err != nil {
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
	data, err := afero.ReadFile(c.fs, artworkFile(c.db.DataDir(), id, kind))
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
