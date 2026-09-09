// Package backup creates, verifies, schedules, and restores Flixr backups.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

const (
	manifestName      = "manifest.json"
	ownerMarker       = ".flixr-backups"
	archiveSuffix     = ".flixr-backup"
	maxManifestBytes  = 1 << 20
	maxArchiveEntries = 10_000
	maxExpandedBytes  = int64(64 << 30)
	spaceReserveBytes = int64(64 << 20)
)

type Entry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	FormatVersion int       `json:"format_version"`
	AppVersion    string    `json:"app_version"`
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
	Entries       []Entry   `json:"entries"`
}

type Source struct {
	DB                *sqlite.DB
	DataDir           string
	MediaRoots        []string
	MediaRootProvider func() ([]string, error)
	SegmentDir        string
	AppVersion        string
}

type Options struct {
	Destination   string
	Now           func() time.Time
	beforeArtwork func(string)
	freeBytes     func(string) (int64, error)
}

type Result struct {
	Path     string
	Size     int64
	Manifest Manifest
}

type sourceEntry struct {
	Entry
	path string
}

func Create(ctx context.Context, source Source, options Options) (Result, error) {
	if source.DB == nil || source.DataDir == "" {
		return Result{}, errors.New("backup source is incomplete")
	}
	mediaRoots := source.MediaRoots
	var err error
	if source.MediaRootProvider != nil {
		mediaRoots, err = source.MediaRootProvider()
		if err != nil {
			return Result{}, errors.New("media roots cannot be checked for backup confinement")
		}
	}
	destination, err := authorizeDestination(options.Destination, append([]string{source.DataDir, source.SegmentDir}, mediaRoots...))
	if err != nil {
		return Result{}, err
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	id, err := backupID()
	if err != nil {
		return Result{}, fmt.Errorf("create backup id: %w", err)
	}
	snapshot := filepath.Join(destination, ".flixr-snapshot-"+id+".partial")
	partial := filepath.Join(destination, ".flixr-backup-"+id+".partial")
	defer os.Remove(snapshot)
	defer os.Remove(partial)
	if err := source.DB.OnlineBackup(ctx, snapshot); err != nil {
		return Result{}, fmt.Errorf("snapshot sqlite: %w", err)
	}
	entries, schemaVersion, err := snapshotEntries(ctx, snapshot, source.DataDir, options.beforeArtwork)
	if err != nil {
		return Result{}, err
	}
	manifest := Manifest{FormatVersion: 1, AppVersion: source.AppVersion, SchemaVersion: schemaVersion, CreatedAt: now().UTC(), Entries: make([]Entry, len(entries))}
	var expanded int64
	for i := range entries {
		manifest.Entries[i] = entries[i].Entry
		if expanded > maxExpandedBytes-entries[i].Size {
			return Result{}, errors.New("backup contents exceed the supported size")
		}
		expanded += entries[i].Size
	}
	freeBytes := availableBytes
	if options.freeBytes != nil {
		freeBytes = options.freeBytes
	}
	available, err := freeBytes(destination)
	if err != nil {
		return Result{}, fmt.Errorf("check backup destination space: %w", err)
	}
	if required := expanded + spaceReserveBytes; available < required {
		return Result{}, errors.New("backup destination does not have enough available space")
	}
	if err := writeArchive(ctx, partial, manifest, entries); err != nil {
		return Result{}, err
	}
	verified, err := Verify(ctx, partial)
	if err != nil {
		return Result{}, fmt.Errorf("verify new backup: %w", err)
	}
	if verified.SchemaVersion != manifest.SchemaVersion || len(verified.Entries) != len(manifest.Entries) {
		return Result{}, errors.New("new backup verification did not match its manifest")
	}
	final := filepath.Join(destination, "flixr-backup-"+manifest.CreatedAt.Format("20060102T150405Z")+"-"+id+archiveSuffix)
	if err := os.Rename(partial, final); err != nil {
		return Result{}, fmt.Errorf("publish verified backup: %w", err)
	}
	if err := syncDirectory(destination); err != nil {
		return Result{}, fmt.Errorf("sync backup destination: %w", err)
	}
	info, err := os.Stat(final)
	if err != nil {
		return Result{}, fmt.Errorf("inspect published backup: %w", err)
	}
	return Result{Path: final, Size: info.Size(), Manifest: manifest}, nil
}

func snapshotEntries(ctx context.Context, snapshot, dataDir string, beforeArtwork func(string)) ([]sourceEntry, int, error) {
	databaseEntry, err := inspectSourceFile(ctx, "database/flixr.db", snapshot)
	if err != nil {
		return nil, 0, fmt.Errorf("inspect sqlite snapshot: %w", err)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(snapshot))
	if err != nil {
		return nil, 0, fmt.Errorf("open sqlite snapshot: %w", err)
	}
	defer db.Close()
	var schemaVersion int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return nil, 0, fmt.Errorf("read snapshot schema: %w", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT object_name FROM catalog_artwork WHERE object_name<>'' ORDER BY object_name`)
	if err != nil {
		return nil, 0, fmt.Errorf("read snapshot artwork references: %w", err)
	}
	defer rows.Close()
	entries := []sourceEntry{databaseEntry}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, 0, err
		}
		if filepath.Base(name) != name || name == "." || name == ".." {
			return nil, 0, errors.New("snapshot contains an invalid artwork object name")
		}
		path := filepath.Join(dataDir, "artwork", "objects", name)
		if beforeArtwork != nil {
			beforeArtwork(path)
		}
		entry, err := inspectSourceFile(ctx, filepath.ToSlash(filepath.Join("artwork", "objects", name)), path)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			return nil, 0, errors.New("a referenced artwork object became unavailable")
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return entries, schemaVersion, nil
}

func inspectSourceFile(ctx context.Context, name, path string) (sourceEntry, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return sourceEntry{}, errors.New("backup source entry is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return sourceEntry{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := copyContext(ctx, hash, file, info.Size()); err != nil {
		return sourceEntry{}, err
	}
	return sourceEntry{Entry: Entry{Name: name, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, path: path}, nil
}

func writeArchive(ctx context.Context, path string, manifest Manifest, entries []sourceEntry) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create backup partial: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := writeTarBytes(tarWriter, manifestName, manifestBytes); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tarWriter.WriteHeader(&tar.Header{Name: entry.Name, Mode: 0o600, Size: entry.Size, Typeflag: tar.TypeReg, ModTime: manifest.CreatedAt}); err != nil {
			return fmt.Errorf("write backup entry header: %w", err)
		}
		source, err := os.Open(entry.path)
		if err != nil {
			return errors.New("a backup source entry became unavailable")
		}
		hash := sha256.New()
		written, copyErr := copyContext(ctx, io.MultiWriter(tarWriter, hash), source, entry.Size)
		closeErr := source.Close()
		if copyErr != nil || closeErr != nil || written != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return errors.New("a backup source entry changed while it was being archived")
		}
	}
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("finish backup archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("finish backup compression: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync backup partial: %w", err)
	}
	return nil
}

func writeTarBytes(writer *tar.Writer, name string, data []byte) error {
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}

func backupID() (string, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func availableBytes(path string) (int64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, err
	}
	if stats.Bavail > uint64(^uint64(0)>>1)/uint64(stats.Bsize) {
		return int64(^uint64(0) >> 1), nil
	}
	return int64(stats.Bavail) * int64(stats.Bsize), nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader, limit int64) (int64, error) {
	buffer := make([]byte, 128<<10)
	var written int64
	for written < limit {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		want := int64(len(buffer))
		if remaining := limit - written; remaining < want {
			want = remaining
		}
		n, readErr := source.Read(buffer[:want])
		if n > 0 {
			m, writeErr := destination.Write(buffer[:n])
			written += int64(m)
			if writeErr != nil {
				return written, writeErr
			}
			if m != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && written == limit {
				return written, nil
			}
			return written, readErr
		}
	}
	return written, nil
}

func readOnlyDSN(path string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=foreign_keys(1)"}).String()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func authorizeDestination(path string, excluded []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("backup destination must be an absolute path")
	}
	clean := filepath.Clean(path)
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup destination must be an existing directory, not a symlink")
	}
	for _, root := range excluded {
		if root != "" && pathsOverlap(clean, root) {
			return "", errors.New("backup destination must be outside Flixr data, media, and playback cache paths")
		}
	}
	entries, err := os.ReadDir(clean)
	if err != nil {
		return "", errors.New("backup destination is not readable")
	}
	if len(entries) == 0 {
		marker := filepath.Join(clean, ownerMarker)
		if err := os.WriteFile(marker, []byte("Flixr backup destination v1\n"), 0o600); err != nil {
			return "", errors.New("backup destination is not writable")
		}
		if err := syncDirectory(clean); err != nil {
			return "", errors.New("backup destination cannot be synchronized")
		}
		return clean, nil
	}
	marker := filepath.Join(clean, ownerMarker)
	markerInfo, err := os.Lstat(marker)
	if err != nil || !markerInfo.Mode().IsRegular() {
		return "", errors.New("backup destination is not empty or marked as Flixr-owned")
	}
	return clean, nil
}

func pathsOverlap(a, b string) bool {
	aResolved, aErr := filepath.EvalSymlinks(filepath.Clean(a))
	bResolved, bErr := filepath.EvalSymlinks(filepath.Clean(b))
	if aErr == nil {
		a = aResolved
	}
	if bErr == nil {
		b = bResolved
	}
	return within(a, b) || within(b, a)
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sortedManifestEntries(manifest Manifest) []Entry {
	entries := append([]Entry(nil), manifest.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}
