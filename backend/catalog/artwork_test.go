package catalog

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestArtworkSizedDeduplicatesAndKeepsOriginal(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	sourceImage := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	for x := 0; x < 800; x++ {
		for y := 0; y < 1200; y++ {
			sourceImage.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var source bytes.Buffer
	if err := jpeg.Encode(&source, sourceImage, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.cacheArtwork("film", "poster", Artwork{Bytes: source.Bytes(), ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			data, contentType, err := c.ArtworkSized("film", "poster", 200)
			if err != nil || contentType != "image/jpeg" {
				t.Errorf("derivative = %q %v", contentType, err)
				return
			}
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil || decoded.Bounds().Dx() != 200 {
				t.Errorf("derivative bounds = %v, %v", decoded.Bounds(), err)
			}
		}()
	}
	group.Wait()
	original, _, err := c.Artwork("film", "poster")
	if err != nil || !bytes.Equal(original, source.Bytes()) {
		t.Fatalf("original changed: %v", err)
	}
}

func TestArtworkSizedFallsBackForInvalidCachedImage(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.cacheArtwork("film", "poster", Artwork{Bytes: []byte("not an image"), ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	data, contentType, err := c.ArtworkSized("film", "poster", 200)
	if err != nil || string(data) != "not an image" || contentType != "image/jpeg" {
		t.Fatalf("fallback = %q %q %v", data, contentType, err)
	}
}

func TestArtworkMaintenanceEvictsOnlyExpiredDerivatives(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.cacheArtwork("film", "poster", Artwork{Bytes: []byte("durable"), ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(db.DataDir(), "artwork", "derivatives")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-derivativeMaxAge - time.Hour)
	for i := range maintenanceMaxFiles + 1 {
		path := filepath.Join(dir, "film-poster-"+strconv.Itoa(i)+".jpg")
		if err := os.WriteFile(path, []byte("derivative"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	c.artworkMu.Lock()
	c.cleanupDerivativesLocked(time.Now())
	c.artworkMu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != maintenanceMaxFiles {
		t.Fatalf("derivatives = %d, %v", len(entries), err)
	}
	data, _, err := c.Artwork("film", "poster")
	if err != nil || string(data) != "durable" {
		t.Fatalf("original = %q, %v", data, err)
	}
}
