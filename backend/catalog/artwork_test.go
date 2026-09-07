package catalog

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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
	"github.com/spf13/afero"
)

type failingArtworkFS struct {
	afero.Fs
	openErr, removeErr error
}

func (f failingArtworkFS) Open(name string) (afero.File, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.Fs.Open(name)
}
func (f failingArtworkFS) Remove(name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	return f.Fs.Remove(name)
}

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
			data, contentType, err := c.ArtworkSized(context.Background(), "film", "poster", 200)
			if err != nil || contentType != "image/jpeg" {
				t.Errorf("derivative = %q %v", contentType, err)
				return
			}
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Errorf("decode derivative: %v", err)
			} else if decoded.Bounds().Dx() != 240 {
				t.Errorf("derivative width = %d", decoded.Bounds().Dx())
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
	data, contentType, err := c.ArtworkSized(context.Background(), "film", "poster", 200)
	if err != nil || string(data) != "not an image" || contentType != "image/jpeg" {
		t.Fatalf("fallback = %q %q %v", data, contentType, err)
	}
}

func TestArtworkSizedRejectsUnsafeDimensionsAndCancelledWait(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	// A PNG header is enough for DecodeConfig; no pixel buffer may be allocated.
	unsafe := []byte{'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0x50, 0, 0, 0, 0x50, 8, 2, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(unsafe[16:20], 5000)
	binary.BigEndian.PutUint32(unsafe[20:24], 5000)
	if _, err := c.cacheArtwork("film", "poster", Artwork{Bytes: unsafe, ContentType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	data, _, err := c.ArtworkSized(context.Background(), "film", "poster", 200)
	if err != nil || !bytes.Equal(data, unsafe) {
		t.Fatalf("unsafe dimensions fallback = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, width := range []int{0, 200} {
		data, _, err = c.ArtworkSized(ctx, "film", "poster", width)
		if !errors.Is(err, context.Canceled) || !bytes.Equal(data, unsafe) {
			t.Fatalf("cancel at width %d = %v", width, err)
		}
	}
}

func TestArtworkSizedInvalidatesDerivativeWhenOriginalChanges(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	imageA := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	imageA.Set(1, 1, color.RGBA{R: 255, A: 255})
	var first bytes.Buffer
	if err := jpeg.Encode(&first, imageA, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.cacheArtwork("film", "poster", Artwork{Bytes: first.Bytes(), ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	before, _, err := c.ArtworkSized(context.Background(), "film", "poster", 200)
	if err != nil {
		t.Fatal(err)
	}
	imageA.Set(1, 1, color.RGBA{B: 255, A: 255})
	var second bytes.Buffer
	if err := jpeg.Encode(&second, imageA, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.cacheArtwork("film", "poster", Artwork{Bytes: second.Bytes(), ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	after, _, err := c.ArtworkSized(context.Background(), "film", "poster", 200)
	if err != nil || bytes.Equal(before, after) {
		t.Fatalf("replacement derivative stale: %v", err)
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
	if err != nil || len(entries) != 0 {
		t.Fatalf("derivatives = %d, %v", len(entries), err)
	}
	data, _, err := c.Artwork("film", "poster")
	if err != nil || string(data) != "durable" {
		t.Fatalf("original = %q, %v", data, err)
	}
}

func TestArtworkMaintenanceBoundsFreshCacheBeyondOneBatch(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(db.DataDir(), "artwork", "derivatives")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := range maintenanceBatch + 44 {
		if err := os.WriteFile(filepath.Join(dir, "f-"+strconv.Itoa(i)+".jpg"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		c.artworkMu.Lock()
		c.cleanupDerivativesLocked(time.Now())
		c.artworkMu.Unlock()
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > maintenanceMaxFiles {
		t.Fatalf("fresh cache = %d, %v", len(entries), err)
	}
}

func TestArtworkSizedRefusesCacheAdmissionAtEntryBudget(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	c.artworkMu.Lock()
	c.derivativeReady = true
	c.artworkMu.Unlock()
	sourceImage := image.NewRGBA(image.Rect(0, 0, 400, 600))
	var source bytes.Buffer
	if err := jpeg.Encode(&source, sourceImage, nil); err != nil {
		t.Fatal(err)
	}
	for i := range maintenanceMaxFiles + 6 {
		id := strconv.Itoa(i)
		if _, err := c.cacheArtwork(id, "poster", Artwork{Bytes: source.Bytes(), ContentType: "image/jpeg"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.ArtworkSized(context.Background(), id, "poster", 160); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(db.DataDir(), "artwork", "derivatives"))
	if err != nil || len(entries) != maintenanceMaxFiles {
		t.Fatalf("admitted derivatives = %d, %v", len(entries), err)
	}
}

func TestArtworkMaintenanceStopsOnShutdown(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := c.ArtworkMaintenanceStatus(); status.Outcome != "complete" || status.LastRun.IsZero() {
		t.Fatalf("maintenance status = %#v", status)
	}
}

func TestArtworkMaintenanceDoesNotAdmitAfterInventoryErrors(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.fs = failingArtworkFS{Fs: afero.NewOsFs(), openErr: os.ErrPermission}
	c.artworkMu.Lock()
	c.derivativeReady = false
	err = c.cleanupDerivativesLocked(time.Now())
	ready := c.derivativeReady
	c.artworkMu.Unlock()
	if err == nil || ready {
		t.Fatalf("inventory error admitted cache: %v ready=%v", err, ready)
	}
}

func BenchmarkArtworkSizedCold30Warm30(b *testing.B) {
	db, err := sqlite.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		b.Fatal(err)
	}
	sourceImage := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	var source bytes.Buffer
	if err := jpeg.Encode(&source, sourceImage, nil); err != nil {
		b.Fatal(err)
	}
	for i := range 30 {
		if _, err := c.cacheArtwork(strconv.Itoa(i), "poster", Artwork{Bytes: source.Bytes(), ContentType: "image/jpeg"}); err != nil {
			b.Fatal(err)
		}
	}
	run := func() {
		for i := range 30 {
			if _, _, err := c.ArtworkSized(context.Background(), strconv.Itoa(i), "poster", 240); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("cold30", func(b *testing.B) {
		for range b.N {
			b.StopTimer()
			_ = os.RemoveAll(filepath.Join(db.DataDir(), "artwork", "derivatives"))
			b.StartTimer()
			run()
		}
	})
	b.Run("warm30", func(b *testing.B) {
		run()
		b.ResetTimer()
		for range b.N {
			run()
		}
	})
}
