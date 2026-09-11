package catalog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestLocalArtworkStoreRoundTripAndOwnership(t *testing.T) {
	fs := afero.NewMemMapFs()
	first := newLocalArtworkStore(fs, "/data")
	second := newLocalArtworkStore(fs, "/data")

	poster, err := first.Stage(context.Background(), Artwork{Bytes: []byte("poster"), ContentType: "image/jpeg"})
	if err != nil {
		t.Fatal(err)
	}
	backdrop, err := first.Stage(context.Background(), Artwork{Bytes: []byte("backdrop"), ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := second.Stage(context.Background(), Artwork{Bytes: []byte("other"), ContentType: "image/webp"})
	if err != nil {
		t.Fatal(err)
	}

	for descriptor, want := range map[localArtworkDescriptor]Artwork{
		poster:   {Bytes: []byte("poster"), ContentType: "image/jpeg"},
		backdrop: {Bytes: []byte("backdrop"), ContentType: "image/png"},
	} {
		got, err := first.Read(context.Background(), descriptor)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes, want.Bytes) || got.ContentType != want.ContentType {
			t.Fatalf("Read(%+v) = %+v, want %+v", descriptor, got, want)
		}
	}
	if _, err := first.Read(context.Background(), other); err == nil {
		t.Fatal("Read accepted a descriptor owned by another store")
	}
	forged := localArtworkDescriptor{Filename: filepath.Base(poster.Filename), ContentType: poster.ContentType}
	forged.Filename = "artwork-forged"
	if _, err := first.Read(context.Background(), forged); err == nil {
		t.Fatal("Read accepted an unregistered filename")
	}
	if _, err := first.Read(context.Background(), localArtworkDescriptor{Filename: "../outside", ContentType: "image/jpeg"}); err == nil {
		t.Fatal("Read accepted a path outside the store")
	}

	if got := reflect.TypeOf(localArtworkDescriptor{}); got.NumField() != 2 || got.Field(0).Type.Kind() != reflect.String || got.Field(1).Type.Kind() != reflect.String {
		t.Fatalf("descriptor retains more than two small scalar fields: %v", got)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Read(context.Background(), other); err != nil {
		t.Fatalf("closing one store affected another: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLocalArtworkStoreBoundsCancellationAndFailures(t *testing.T) {
	t.Run("stage limit", func(t *testing.T) {
		store := newLocalArtworkStore(afero.NewMemMapFs(), "/data")
		_, err := store.Stage(context.Background(), Artwork{Bytes: make([]byte, maxArtworkSource+1), ContentType: "image/jpeg"})
		if err == nil {
			t.Fatal("Stage accepted oversized artwork")
		}
	})

	t.Run("cancelled stage is lazy", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		store := newLocalArtworkStore(fs, "/data")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := store.Stage(ctx, Artwork{Bytes: []byte("image"), ContentType: "image/jpeg"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Stage error = %v, want context.Canceled", err)
		}
		if _, err := fs.Stat(filepath.Join("/data", localArtworkStagingDir)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled Stage created staging directory: %v", err)
		}
	})

	t.Run("write failure removes partial file", func(t *testing.T) {
		base := afero.NewMemMapFs()
		store := newLocalArtworkStore(localArtworkFaultFS{Fs: base, failWrite: true}, "/data")
		if _, err := store.Stage(context.Background(), Artwork{Bytes: []byte("image"), ContentType: "image/jpeg"}); err == nil {
			t.Fatal("Stage succeeded despite write failure")
		}
		if store.dir != "" {
			entries, err := afero.ReadDir(base, store.dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("partial entries = %v", entries)
			}
		}
	})

	t.Run("cancellation during write removes partial file", func(t *testing.T) {
		base := afero.NewMemMapFs()
		ctx, cancel := context.WithCancel(context.Background())
		store := newLocalArtworkStore(localArtworkCancelWriteFS{Fs: base, cancel: cancel}, "/data")
		_, err := store.Stage(ctx, Artwork{Bytes: make([]byte, 128<<10), ContentType: "image/jpeg"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stage error = %v, want context.Canceled", err)
		}
		entries, err := afero.ReadDir(base, store.dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("partial entries = %v", entries)
		}
	})

	t.Run("read limit after growth", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		store := newLocalArtworkStore(fs, "/data")
		descriptor, err := store.Stage(context.Background(), Artwork{Bytes: []byte("small"), ContentType: "image/jpeg"})
		if err != nil {
			t.Fatal(err)
		}
		if err := afero.WriteFile(fs, filepath.Join(store.dir, descriptor.Filename), make([]byte, maxArtworkSource+1), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Read(context.Background(), descriptor); err == nil {
			t.Fatal("Read accepted artwork grown beyond the limit")
		}
	})

	t.Run("cancelled and failed read", func(t *testing.T) {
		base := afero.NewMemMapFs()
		faults := &localArtworkFaultFS{Fs: base}
		store := newLocalArtworkStore(faults, "/data")
		descriptor, err := store.Stage(context.Background(), Artwork{Bytes: []byte("image"), ContentType: "image/jpeg"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := store.Read(ctx, descriptor); !errors.Is(err, context.Canceled) {
			t.Fatalf("Read error = %v, want context.Canceled", err)
		}
		faults.failRead = true
		if _, err := store.Read(context.Background(), descriptor); err == nil {
			t.Fatal("Read succeeded despite open failure")
		}
	})
}

func TestLocalArtworkStorePermissionsAndCleanupConfinement(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "keep")
	if err := os.WriteFile(outsideFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	fs := afero.NewOsFs()
	store := newLocalArtworkStore(fs, dataDir)
	descriptor, err := store.Stage(context.Background(), Artwork{Bytes: []byte("image"), ContentType: "image/jpeg"})
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(filepath.Join(store.dir, descriptor.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory permissions = %o", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file permissions = %o", got)
	}

	stagingBase := filepath.Join(dataDir, localArtworkStagingDir)
	orphan := filepath.Join(stagingBase, "scan-orphan")
	if err := os.Mkdir(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "stale"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkChild := filepath.Join(stagingBase, "scan-symlink")
	if err := os.Symlink(outside, symlinkChild); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(stagingBase, "keep-unrelated")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanupLocalArtworkOrphans(fs, dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("cleanup touched outside file: %v", err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup left orphan: %v", err)
	}
	if _, err := os.Lstat(symlinkChild); err != nil {
		t.Fatalf("cleanup removed scan-like symlink: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("cleanup removed unrelated staging entry: %v", err)
	}
}

func TestCleanupLocalArtworkOrphansRejectsSymlinkBase(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "scan-orphan"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dataDir, localArtworkStagingDir)); err != nil {
		t.Fatal(err)
	}

	err := cleanupLocalArtworkOrphans(afero.NewOsFs(), dataDir)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("cleanup error = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "scan-orphan")); err != nil {
		t.Fatalf("cleanup followed symlink base: %v", err)
	}
}

type localArtworkFaultFS struct {
	afero.Fs
	failWrite bool
	failRead  bool
}

func (f localArtworkFaultFS) Open(name string) (afero.File, error) {
	if f.failRead {
		return nil, errors.New("read failure")
	}
	return f.Fs.Open(name)
}

func (f localArtworkFaultFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	file, err := f.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 && f.failWrite {
		return localArtworkFailWriteFile{File: file}, nil
	}
	return file, nil
}

type localArtworkFailWriteFile struct{ afero.File }

func (localArtworkFailWriteFile) Write([]byte) (int, error) {
	return 0, errors.New("write failure")
}

type localArtworkCancelWriteFS struct {
	afero.Fs
	cancel context.CancelFunc
}

func (f localArtworkCancelWriteFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	file, err := f.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		return file, nil
	}
	return &localArtworkCancelWriteFile{File: file, cancel: f.cancel}, nil
}

type localArtworkCancelWriteFile struct {
	afero.File
	cancel context.CancelFunc
}

func (f *localArtworkCancelWriteFile) Write(data []byte) (int, error) {
	n, err := f.File.Write(data)
	f.cancel()
	return n, err
}
