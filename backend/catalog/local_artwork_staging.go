package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/afero"
)

const localArtworkStagingDir = ".local-artwork-staging"

type localArtworkDescriptor struct {
	Filename    string
	ContentType string
}

type localArtworkStore struct {
	fs      afero.Fs
	dataDir string

	mu     sync.Mutex
	dir    string
	files  map[string]string
	closed bool
}

func newLocalArtworkStore(fs afero.Fs, dataDir string) *localArtworkStore {
	return &localArtworkStore{fs: fs, dataDir: dataDir}
}

func (s *localArtworkStore) Stage(ctx context.Context, artwork Artwork) (localArtworkDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return localArtworkDescriptor{}, err
	}
	if s.closed {
		return localArtworkDescriptor{}, errors.New("local artwork store is closed")
	}
	if len(artwork.Bytes) == 0 {
		return localArtworkDescriptor{}, errors.New("local artwork is empty")
	}
	if len(artwork.Bytes) > maxArtworkSource {
		return localArtworkDescriptor{}, fmt.Errorf("local artwork exceeds %d byte limit", maxArtworkSource)
	}
	if !allowedArtworkContentType(artwork.ContentType) {
		return localArtworkDescriptor{}, fmt.Errorf("unsupported local artwork content type %q", artwork.ContentType)
	}
	if err := s.ensureDir(); err != nil {
		return localArtworkDescriptor{}, err
	}

	file, err := afero.TempFile(s.fs, s.dir, "artwork-")
	if err != nil {
		return localArtworkDescriptor{}, fmt.Errorf("create local artwork staging file: %w", err)
	}
	name := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = s.fs.Remove(name)
		}
	}()
	if err := s.fs.Chmod(name, 0o600); err != nil {
		return localArtworkDescriptor{}, fmt.Errorf("secure local artwork staging file: %w", err)
	}
	if err := writeWithContext(ctx, file, artwork.Bytes); err != nil {
		return localArtworkDescriptor{}, fmt.Errorf("write local artwork staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		return localArtworkDescriptor{}, fmt.Errorf("close local artwork staging file: %w", err)
	}

	descriptor := localArtworkDescriptor{
		Filename:    filepath.Base(name),
		ContentType: artwork.ContentType,
	}
	s.files[descriptor.Filename] = descriptor.ContentType
	remove = false
	return descriptor, nil
}

func (s *localArtworkStore) Read(ctx context.Context, descriptor localArtworkDescriptor) (Artwork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Artwork{}, err
	}
	if s.closed {
		return Artwork{}, errors.New("local artwork store is closed")
	}
	contentType, ok := s.files[descriptor.Filename]
	if !ok || contentType != descriptor.ContentType || !validLocalArtworkFilename(descriptor.Filename) {
		return Artwork{}, errors.New("local artwork descriptor does not belong to this store")
	}

	file, err := s.fs.Open(filepath.Join(s.dir, descriptor.Filename))
	if err != nil {
		return Artwork{}, fmt.Errorf("open staged local artwork: %w", err)
	}
	defer file.Close()

	var data bytes.Buffer
	limited := io.LimitReader(file, int64(maxArtworkSource)+1)
	if err := copyWithContext(ctx, &data, limited); err != nil {
		return Artwork{}, fmt.Errorf("read staged local artwork: %w", err)
	}
	if data.Len() > maxArtworkSource {
		return Artwork{}, fmt.Errorf("staged local artwork exceeds %d byte limit", maxArtworkSource)
	}
	return Artwork{Bytes: data.Bytes(), ContentType: contentType}, nil
}

func (s *localArtworkStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	if s.dir != "" {
		if err := s.fs.RemoveAll(s.dir); err != nil {
			return fmt.Errorf("remove local artwork staging directory: %w", err)
		}
	}
	s.closed = true
	s.files = nil
	return nil
}

// cleanupLocalArtworkOrphans is a startup operation. Call it before admitting
// scans so every scan-* directory below the private base is known to be stale.
func cleanupLocalArtworkOrphans(fs afero.Fs, dataDir string) error {
	base := filepath.Join(dataDir, localArtworkStagingDir)
	info, err := lstat(fs, base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect local artwork staging directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("local artwork staging directory is a symlink")
	}
	if !info.IsDir() {
		return errors.New("local artwork staging path is not a directory")
	}

	entries, err := afero.ReadDir(fs, base)
	if err != nil {
		return fmt.Errorf("read local artwork staging directory: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "scan-") {
			continue
		}
		path := filepath.Join(base, entry.Name())
		child, err := lstat(fs, path)
		if err != nil {
			return fmt.Errorf("inspect local artwork orphan %q: %w", entry.Name(), err)
		}
		if child.Mode()&os.ModeSymlink != 0 || !child.IsDir() {
			continue
		}
		if err := fs.RemoveAll(path); err != nil {
			return fmt.Errorf("remove local artwork orphan %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *localArtworkStore) ensureDir() error {
	if s.fs == nil {
		return errors.New("local artwork filesystem is nil")
	}
	if s.dataDir == "" {
		return errors.New("local artwork data directory is empty")
	}
	if s.dir != "" {
		return nil
	}

	base := filepath.Join(s.dataDir, localArtworkStagingDir)
	if info, err := lstat(s.fs, base); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("local artwork staging path is not a directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect local artwork staging directory: %w", err)
	}
	if err := s.fs.MkdirAll(base, 0o700); err != nil {
		return fmt.Errorf("create local artwork staging directory: %w", err)
	}
	if err := s.fs.Chmod(base, 0o700); err != nil {
		return fmt.Errorf("secure local artwork staging directory: %w", err)
	}
	info, err := lstat(s.fs, base)
	if err != nil {
		return fmt.Errorf("inspect created local artwork staging directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("local artwork staging directory is not a private directory")
	}

	dir, err := afero.TempDir(s.fs, base, "scan-")
	if err != nil {
		return fmt.Errorf("create local artwork scan directory: %w", err)
	}
	if err := s.fs.Chmod(dir, 0o700); err != nil {
		_ = s.fs.RemoveAll(dir)
		return fmt.Errorf("secure local artwork scan directory: %w", err)
	}
	s.dir = dir
	s.files = make(map[string]string)
	return nil
}

func validLocalArtworkFilename(name string) bool {
	return name != "" && name == filepath.Base(name) && strings.HasPrefix(name, "artwork-")
}

func writeWithContext(ctx context.Context, dst io.Writer, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := data
		if len(chunk) > 64<<10 {
			chunk = chunk[:64<<10]
		}
		n, err := dst.Write(chunk)
		if err != nil {
			return err
		}
		if n != len(chunk) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := src.Read(buffer)
		if n > 0 {
			if _, writeErr := dst.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func lstat(fs afero.Fs, name string) (os.FileInfo, error) {
	lstater, ok := fs.(afero.Lstater)
	if ok {
		info, _, err := lstater.LstatIfPossible(name)
		return info, err
	}
	// Filesystems that support symlinks expose afero.Lstater. For simpler
	// injected filesystems, Stat is sufficient because links cannot be made.
	return fs.Stat(name)
}
