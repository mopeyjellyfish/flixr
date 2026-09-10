package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestCreateUsesOnlineSnapshotAndOnlyReferencedArtwork(t *testing.T) {
	dataDir := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	objects := filepath.Join(dataDir, "artwork", "objects")
	if err := os.MkdirAll(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objects, "kept-image"), []byte("kept artwork"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objects, "orphan-image"), []byte("excluded"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "artwork", "derivatives"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "artwork", "derivatives", "cached.jpg"), []byte("excluded"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_artwork(catalog_id,kind,content_type,object_name) VALUES('film','poster','image/jpeg','kept-image')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('backup-proof','durable')`); err != nil {
		t.Fatal(err)
	}

	result, err := Create(context.Background(), Source{DB: db, DataDir: dataDir, AppVersion: "v0.test"}, Options{
		Destination: destination,
		Now:         func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(context.Background(), result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != 1 || manifest.AppVersion != "v0.test" || manifest.SchemaVersion != sqlite.LatestSchemaVersion {
		t.Fatalf("manifest = %+v", manifest)
	}
	want := map[string]bool{"database/flixr.db": true, "artwork/objects/kept-image": true}
	for _, entry := range manifest.Entries {
		if !want[entry.Name] {
			t.Fatalf("unexpected durable entry %q", entry.Name)
		}
		delete(want, entry.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing durable entries: %#v", want)
	}
	if _, err := os.Stat(filepath.Join(destination, ownerMarker)); err != nil {
		t.Fatalf("destination was not claimed: %v", err)
	}
	partials, _ := filepath.Glob(filepath.Join(destination, "*.partial"))
	if len(partials) != 0 {
		t.Fatalf("successful backup left partials: %v", partials)
	}
}

func TestCreateFailsWithoutPublishingWhenReferencedArtworkDisappears(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	objects := filepath.Join(dataDir, "artwork", "objects")
	if err := os.MkdirAll(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	artwork := filepath.Join(objects, "raced-image")
	if err := os.WriteFile(artwork, []byte("artwork"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_artwork(catalog_id,kind,content_type,object_name) VALUES('film','poster','image/jpeg','raced-image')`); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	removed := false
	_, err = Create(context.Background(), Source{DB: db, DataDir: dataDir}, Options{Destination: destination, beforeArtwork: func(string) {
		if !removed {
			removed = true
			_ = os.Remove(artwork)
		}
	}})
	if err == nil {
		t.Fatal("missing referenced artwork was accepted")
	}
	archives, _ := filepath.Glob(filepath.Join(destination, "*.flixr-backup"))
	if len(archives) != 0 {
		t.Fatalf("failed backup was published: %v", archives)
	}
}

func TestCreateLeavesNoPartialWhenCancelledOrDestinationHasNoSpace(t *testing.T) {
	data := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, test := range []struct {
		name string
		ctx  context.Context
		free func(string) (int64, error)
	}{{"cancelled", cancelledContext(), nil}, {"no-space", context.Background(), func(string) (int64, error) { return 0, nil }}} {
		t.Run(test.name, func(t *testing.T) {
			destination := t.TempDir()
			if _, err := Create(test.ctx, Source{DB: db, DataDir: data}, Options{Destination: destination, freeBytes: test.free}); err == nil {
				t.Fatal("backup succeeded")
			}
			partials, _ := filepath.Glob(filepath.Join(destination, "*.partial"))
			if len(partials) != 0 {
				t.Fatalf("partials=%v", partials)
			}
		})
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestVerifyRejectsFutureSchemaBeforeExtraction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.flixr-backup")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	manifest, _ := json.Marshal(Manifest{FormatVersion: 1, SchemaVersion: sqlite.LatestSchemaVersion + 1, Entries: []Entry{{Name: "database/flixr.db", Size: 0, SHA256: strings.Repeat("0", 64)}}})
	if err = writeTarBytes(tw, manifestName, manifest); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = file.Close()
	if _, err = Verify(context.Background(), path); err == nil {
		t.Fatal("future schema accepted")
	}
}

func TestCreateRejectsOverlappingAndSymlinkDestinations(t *testing.T) {
	data := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	inside := filepath.Join(data, "backups")
	if err = os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = Create(context.Background(), Source{DB: db, DataDir: data}, Options{Destination: inside}); err == nil {
		t.Fatal("overlapping destination accepted")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "backups")
	if err = os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err = Create(context.Background(), Source{DB: db, DataDir: data}, Options{Destination: link}); err == nil {
		t.Fatal("symlink destination accepted")
	}
}

func TestCreateRechecksCurrentMediaRoots(t *testing.T) {
	data := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	destination := t.TempDir()
	source := Source{DB: db, DataDir: data, MediaRootProvider: func() ([]string, error) { return []string{destination}, nil }}
	if _, err = Create(context.Background(), source, Options{Destination: destination}); err == nil {
		t.Fatal("current media root accepted as backup destination")
	}
}

func TestVerifyRejectsCorruptTraversalUnknownDuplicateAndOversizedArchives(t *testing.T) {
	tests := []struct {
		name    string
		entries []tar.Header
		body    []byte
	}{
		{"traversal", []tar.Header{{Name: "../flixr.db", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}}, []byte("x")},
		{"absolute", []tar.Header{{Name: "/flixr.db", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}}, []byte("x")},
		{"unknown", []tar.Header{{Name: "secret.env", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}}, []byte("x")},
		{"duplicate", []tar.Header{{Name: manifestName, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}, {Name: manifestName, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}}, []byte("xx")},
		{"oversized", []tar.Header{{Name: "database/flixr.db", Mode: 0o600, Size: maxExpandedBytes + 1, Typeflag: tar.TypeReg}}, nil},
		{"link", []tar.Header{{Name: "artwork/objects/image", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hostile.flixr-backup")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			gzipWriter := gzip.NewWriter(file)
			tarWriter := tar.NewWriter(gzipWriter)
			body := test.body
			for _, header := range test.entries {
				if err := tarWriter.WriteHeader(&header); err != nil {
					t.Fatal(err)
				}
				if header.Size > 0 && header.Size <= int64(len(body)) {
					if _, err := tarWriter.Write(body[:header.Size]); err != nil {
						t.Fatal(err)
					}
					body = body[header.Size:]
				}
			}
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			_ = file.Close()
			if _, err := Verify(context.Background(), path); err == nil || strings.Contains(err.Error(), path) {
				t.Fatalf("unsafe archive result = %v", err)
			}
		})
	}
}
