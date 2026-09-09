package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func Verify(ctx context.Context, archivePath string) (Manifest, error) {
	info, err := os.Lstat(archivePath)
	if err != nil || !info.Mode().IsRegular() {
		return Manifest{}, errors.New("backup archive must be a regular file")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, errors.New("backup archive cannot be opened")
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, errors.New("backup archive compression is invalid")
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	header, err := tarReader.Next()
	if err != nil || header.Name != manifestName || header.Typeflag != tar.TypeReg || header.Size < 2 || header.Size > maxManifestBytes {
		return Manifest{}, errors.New("backup manifest is missing or invalid")
	}
	var manifest Manifest
	decoder := json.NewDecoder(io.LimitReader(tarReader, maxManifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.FormatVersion != 1 || manifest.SchemaVersion < 1 || manifest.SchemaVersion > sqlite.LatestSchemaVersion || len(manifest.Entries) == 0 || len(manifest.Entries) > maxArchiveEntries {
		return Manifest{}, errors.New("backup manifest version or contents are unsupported")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("backup manifest contains trailing data")
	}
	expected := make(map[string]Entry, len(manifest.Entries))
	var expectedTotal int64
	for _, entry := range manifest.Entries {
		if !allowedArchiveEntry(entry.Name) || entry.Size < 0 || entry.Size > maxExpandedBytes || len(entry.SHA256) != sha256.Size*2 {
			return Manifest{}, errors.New("backup manifest contains an invalid entry")
		}
		if _, duplicate := expected[entry.Name]; duplicate {
			return Manifest{}, errors.New("backup manifest contains duplicate entries")
		}
		if expectedTotal > maxExpandedBytes-entry.Size {
			return Manifest{}, errors.New("backup expands beyond the supported size")
		}
		expectedTotal += entry.Size
		expected[entry.Name] = entry
	}
	tempDir, err := os.MkdirTemp("", "flixr-backup-verify-")
	if err != nil {
		return Manifest{}, errors.New("cannot stage backup verification")
	}
	defer os.RemoveAll(tempDir)
	databasePath := filepath.Join(tempDir, "flixr.db")
	seen := map[string]bool{manifestName: true}
	var expanded int64
	for count := 1; ; count++ {
		if count > maxArchiveEntries+1 {
			return Manifest{}, errors.New("backup archive contains too many entries")
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, errors.New("backup archive is truncated or corrupt")
		}
		if header.Typeflag != tar.TypeReg || !allowedArchiveEntry(header.Name) || seen[header.Name] {
			return Manifest{}, errors.New("backup archive contains an unsafe or duplicate entry")
		}
		entry, ok := expected[header.Name]
		if !ok || header.Size != entry.Size || header.Size < 0 || expanded > maxExpandedBytes-header.Size {
			return Manifest{}, errors.New("backup archive does not match its manifest")
		}
		expanded += header.Size
		hash := sha256.New()
		var writer io.Writer = hash
		var database *os.File
		if header.Name == "database/flixr.db" {
			database, err = os.OpenFile(databasePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return Manifest{}, errors.New("cannot stage backup database verification")
			}
			writer = io.MultiWriter(hash, database)
		}
		written, copyErr := copyContext(ctx, writer, tarReader, header.Size)
		if database != nil {
			copyErr = errors.Join(copyErr, database.Close())
		}
		if copyErr != nil || written != header.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return Manifest{}, errors.New("backup archive checksum verification failed")
		}
		seen[header.Name] = true
	}
	if len(seen) != len(expected)+1 || !seen["database/flixr.db"] {
		return Manifest{}, errors.New("backup archive is missing a manifested entry")
	}
	if err := verifyDatabase(ctx, databasePath, manifest.SchemaVersion); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func allowedArchiveEntry(name string) bool {
	if name == "database/flixr.db" {
		return true
	}
	const prefix = "artwork/objects/"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	base := strings.TrimPrefix(name, prefix)
	return base != "" && base != "." && base != ".." && filepath.Base(base) == base && filepath.ToSlash(name) == name
}

func verifyDatabase(ctx context.Context, path string, manifestVersion int) error {
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return errors.New("backup database cannot be opened")
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("backup database failed integrity verification")
	}
	var foreignKeys int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeys); err != nil || foreignKeys != 0 {
		return errors.New("backup database failed foreign-key verification")
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil || version != manifestVersion {
		return fmt.Errorf("backup database schema does not match its manifest")
	}
	return nil
}
