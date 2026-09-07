package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/afero"
	"golang.org/x/image/draw"
)

const (
	maxArtworkWidth     = 1600
	maxDerivativeBytes  = 128 << 20
	derivativeMaxAge    = 7 * 24 * time.Hour
	maintenanceMaxFiles = 64
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
			c.cleanupDerivativesLocked(time.Now())
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
func derivativeFile(dir, id, kind string, width int) string {
	return filepath.Join(dir, "artwork", "derivatives", id+"-"+kind+"-"+strconv.Itoa(width)+".jpg")
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
func (c *Catalog) ArtworkSized(id, kind string, width int) ([]byte, string, error) {
	original, contentType, err := c.Artwork(id, kind)
	if err != nil || width <= 0 {
		return original, contentType, err
	}
	if width > maxArtworkWidth {
		width = maxArtworkWidth
	}
	key := id + ":" + kind + ":" + strconv.Itoa(width)
	value, err, _ := c.artworkGroup.Do(key, func() (any, error) {
		c.artworkMu.Lock()
		path := derivativeFile(c.db.DataDir(), id, kind, width)
		if data, readErr := afero.ReadFile(c.fs, path); readErr == nil {
			_ = c.fs.Chtimes(path, time.Now(), time.Now())
			c.artworkMu.Unlock()
			return sizedArtwork{data, "image/jpeg"}, nil
		}
		c.artworkMu.Unlock()
		select {
		case artworkWork <- struct{}{}:
			defer func() { <-artworkWork }()
		default:
			return nil, errors.New("artwork resize busy")
		}
		decoded, _, decodeErr := image.Decode(bytes.NewReader(original))
		if decodeErr != nil {
			return nil, fmt.Errorf("decode cached artwork: %w", decodeErr)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() <= width {
			return sizedArtwork{original, contentType}, nil
		}
		height := max(1, bounds.Dy()*width/bounds.Dx())
		resized := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(resized, resized.Bounds(), decoded, bounds, draw.Over, nil)
		var output bytes.Buffer
		if encodeErr := jpeg.Encode(&output, resized, &jpeg.Options{Quality: 82}); encodeErr != nil {
			return nil, fmt.Errorf("encode artwork derivative: %w", encodeErr)
		}
		c.artworkMu.Lock()
		writeErr := c.writeDerivative(path, output.Bytes())
		if writeErr == nil {
			c.cleanupDerivativesLocked(time.Now())
		}
		c.artworkMu.Unlock()
		if writeErr != nil {
			return nil, writeErr
		}
		return sizedArtwork{output.Bytes(), "image/jpeg"}, nil
	})
	if err != nil {
		return original, contentType, nil
	}
	sized := value.(sizedArtwork)
	return sized.data, sized.contentType, nil
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

func (c *Catalog) cleanupDerivativesLocked(now time.Time) {
	dir := filepath.Join(c.db.DataDir(), "artwork", "derivatives")
	entries, err := afero.ReadDir(c.fs, dir)
	if err != nil {
		return
	}
	type candidate struct {
		path string
		info os.FileInfo
	}
	files := make([]candidate, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if strings.HasPrefix(entry.Name(), ".tmp-") && now.Sub(entry.ModTime()) > time.Hour {
			_ = c.fs.Remove(path)
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jpg") {
			continue
		}
		total += entry.Size()
		files = append(files, candidate{path, entry})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().Before(files[j].info.ModTime()) })
	for removed := 0; removed < len(files) && removed < maintenanceMaxFiles && (total > maxDerivativeBytes || (len(files)-removed > maintenanceMaxFiles && now.Sub(files[removed].info.ModTime()) > derivativeMaxAge)); removed++ {
		if err := c.fs.Remove(files[removed].path); err == nil {
			total -= files[removed].info.Size()
		}
	}
}
