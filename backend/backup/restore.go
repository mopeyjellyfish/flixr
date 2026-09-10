package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

const (
	restoreJournalName     = ".flixr-restore-journal.json"
	restoreStagePrefix     = ".flixr-restore-stage-"
	maxRestoreJournalBytes = 4096
)

var errSimulatedRestoreInterruption = errors.New("simulated restore interruption")

type restoreOptions struct {
	interruptAfterPublish bool
	interruptAfterMoves   int
	freeBytes             func(string) (int64, error)
}

type RestoreResult struct {
	SafetyBackup string
}

type restoreJournal struct {
	Phase       string `json:"phase"`
	Stage       string `json:"stage"`
	HadDatabase bool   `json:"had_database"`
	HadArtwork  bool   `json:"had_artwork"`
	HadSidecar  bool   `json:"had_sidecar"`
}

func Restore(ctx context.Context, archivePath, targetDir string) (RestoreResult, error) {
	return restore(ctx, archivePath, targetDir, restoreOptions{})
}

func restore(ctx context.Context, archivePath, targetDir string, options restoreOptions) (result RestoreResult, err error) {
	if err := validateRestoreTarget(targetDir); err != nil {
		return RestoreResult{}, err
	}
	if err := RecoverInterruptedRestore(targetDir); err != nil {
		return RestoreResult{}, fmt.Errorf("recover interrupted restore: %w", err)
	}
	manifest, err := Verify(ctx, archivePath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("validate backup before restore: %w", err)
	}
	required := spaceReserveBytes
	for _, entry := range manifest.Entries {
		if required > maxExpandedBytes-entry.Size {
			return RestoreResult{}, errors.New("restore requires unsupported working space")
		}
		required += entry.Size
	}
	space := availableBytes
	if options.freeBytes != nil {
		space = options.freeBytes
	}
	available, spaceErr := space(targetDir)
	if spaceErr != nil {
		return RestoreResult{}, errors.New("restore target space cannot be checked")
	}
	if available < required {
		return RestoreResult{}, errors.New("restore target does not have enough available space")
	}
	id, err := backupID()
	if err != nil {
		return RestoreResult{}, err
	}
	stage := filepath.Join(targetDir, restoreStagePrefix+id)
	if err := os.Mkdir(stage, 0o700); err != nil {
		return RestoreResult{}, errors.New("cannot create restore staging directory")
	}
	defer func() {
		if !errors.Is(err, errSimulatedRestoreInterruption) {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := extractArchive(ctx, archivePath, stage, manifest); err != nil {
		return RestoreResult{}, err
	}
	if err := prepareStagedInstallation(ctx, stage, targetDir); err != nil {
		return RestoreResult{}, err
	}

	database := filepath.Join(targetDir, "flixr.db")
	artwork := filepath.Join(targetDir, "artwork")
	sidecar := filepath.Join(targetDir, ".playback-segment-dir")
	journal := restoreJournal{Phase: "prepared", Stage: filepath.Base(stage), HadDatabase: regularFileExists(database), HadArtwork: directoryExists(artwork), HadSidecar: regularFileExists(sidecar)}
	if journal.HadDatabase {
		safetyDir := filepath.Join(filepath.Dir(filepath.Clean(targetDir)), ".flixr-restore-safety-"+id)
		if err := os.Mkdir(safetyDir, 0o700); err != nil {
			return RestoreResult{}, errors.New("cannot create pre-restore safety destination")
		}
		removeSafetyOnFailure := true
		defer func() {
			if removeSafetyOnFailure {
				_ = os.RemoveAll(safetyDir)
			}
		}()
		existing, openErr := sqlite.OpenContext(ctx, targetDir)
		if openErr != nil {
			return RestoreResult{}, fmt.Errorf("existing installation cannot be safely backed up: %w", openErr)
		}
		safety, backupErr := Create(ctx, Source{DB: existing, DataDir: targetDir, AppVersion: "pre-restore"}, Options{Destination: safetyDir})
		closeErr := existing.Close()
		if backupErr != nil || closeErr != nil {
			return RestoreResult{}, errors.New("existing installation safety backup failed")
		}
		result.SafetyBackup = safety.Path
		removeSafetyOnFailure = false
	}
	if err := writeRestoreJournal(targetDir, journal); err != nil {
		return RestoreResult{}, err
	}
	rollback := true
	defer func() {
		if rollback && !errors.Is(err, errSimulatedRestoreInterruption) {
			err = errors.Join(err, RecoverInterruptedRestore(targetDir))
		}
	}()
	if err := publishStagedInstallation(targetDir, stage, journal, options.interruptAfterMoves); err != nil {
		return result, err
	}
	if options.interruptAfterPublish {
		return result, errSimulatedRestoreInterruption
	}
	journal.Phase = "published"
	if err := writeRestoreJournal(targetDir, journal); err != nil {
		return result, err
	}
	rollback = false
	if err := RecoverInterruptedRestore(targetDir); err != nil {
		return result, fmt.Errorf("finish restore cleanup: %w", err)
	}
	return result, nil
}

func validateRestoreTarget(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("restore target must be an absolute path")
	}
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("restore target must be an existing directory, not a symlink")
	}
	databasePresent, managedPresent := false, false
	for _, item := range []struct {
		name      string
		directory bool
	}{{"flixr.db", false}, {"artwork", true}, {".playback-segment-dir", false}} {
		entry, entryErr := os.Lstat(filepath.Join(path, item.name))
		if errors.Is(entryErr, os.ErrNotExist) {
			continue
		}
		managedPresent = true
		if item.name == "flixr.db" {
			databasePresent = true
		}
		if entryErr != nil || entry.Mode()&os.ModeSymlink != 0 || (item.directory && !entry.IsDir()) || (!item.directory && !entry.Mode().IsRegular()) {
			return errors.New("restore target contains an unsafe managed entry")
		}
	}
	if managedPresent && !databasePresent {
		return errors.New("restore target has managed data without a recoverable Flixr database")
	}
	return nil
}

func extractArchive(ctx context.Context, archivePath, stage string, manifest Manifest) error {
	expected := make(map[string]Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		expected[entry.Name] = entry
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return errors.New("cannot reopen verified backup")
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return errors.New("cannot reopen verified backup compression")
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	seen := map[string]bool{}
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || header.Typeflag != tar.TypeReg {
			return errors.New("verified backup changed before extraction")
		}
		if header.Name == manifestName {
			if _, err := io.Copy(io.Discard, tarReader); err != nil {
				return errors.New("verified backup manifest changed before extraction")
			}
			continue
		}
		entry, ok := expected[header.Name]
		if !ok || seen[header.Name] || header.Size != entry.Size {
			return errors.New("verified backup changed before extraction")
		}
		var destination string
		if header.Name == "database/flixr.db" {
			destination = filepath.Join(stage, "flixr.db")
		} else {
			destination = filepath.Join(stage, "artwork", "objects", filepath.Base(header.Name))
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return errors.New("cannot create restore staging path")
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return errors.New("cannot create staged restore entry")
		}
		hash := sha256.New()
		written, copyErr := copyContext(ctx, io.MultiWriter(file, hash), tarReader, header.Size)
		copyErr = errors.Join(copyErr, file.Sync(), file.Close())
		if copyErr != nil || written != header.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return errors.New("backup changed while it was being staged")
		}
		seen[header.Name] = true
	}
	if len(seen) != len(expected) {
		return errors.New("staged restore is missing a backup entry")
	}
	return nil
}

func prepareStagedInstallation(ctx context.Context, stage, target string) error {
	db, err := sqlite.OpenContext(ctx, stage)
	if err != nil {
		return fmt.Errorf("staged backup database cannot be migrated: %w", err)
	}
	segmentDir := filepath.Join(target, "segments")
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('playback_segment_dir',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, segmentDir); err != nil {
		db.Close()
		return errors.New("staged playback cache path cannot be remapped")
	}
	if _, err := db.Exec(`UPDATE backup_policy SET enabled=0,destination='',next_run_at=NULL,last_status='never',last_message='',last_verified_at=NULL,last_verified_file='' WHERE id=1`); err != nil {
		db.Close()
		return errors.New("staged backup destination cannot be cleared")
	}
	// Screen and playback authority is runtime-only; sessions is the only
	// persisted bearer authority and must never be resurrected by a restore.
	if _, err := db.Exec(`DELETE FROM sessions`); err != nil {
		db.Close()
		return errors.New("staged authentication sessions cannot be cleared")
	}
	if err := db.Close(); err != nil {
		return errors.New("staged backup database cannot be finalized")
	}
	if err := os.WriteFile(filepath.Join(stage, ".playback-segment-dir"), []byte(segmentDir+"\n"), 0o600); err != nil {
		return errors.New("staged playback cache sidecar cannot be written")
	}
	return nil
}

func writeRestoreJournal(target string, journal restoreJournal) error {
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	temp := filepath.Join(target, restoreJournalName+".partial")
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return errors.New("restore journal cannot be written")
	}
	file, err := os.OpenFile(temp, os.O_WRONLY, 0)
	if err != nil {
		return errors.New("restore journal cannot be synchronized")
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("restore journal cannot be synchronized")
	}
	if err := os.Rename(temp, filepath.Join(target, restoreJournalName)); err != nil {
		return errors.New("restore journal cannot be published")
	}
	return syncDirectory(target)
}

func publishStagedInstallation(target, stage string, journal restoreJournal, interruptAfter int) error {
	moves := []struct {
		live, prior, staged string
		existed             bool
	}{
		{filepath.Join(target, "flixr.db"), filepath.Join(target, ".flixr-restore-prior.db"), filepath.Join(stage, "flixr.db"), journal.HadDatabase},
		{filepath.Join(target, "artwork"), filepath.Join(target, ".flixr-restore-prior-artwork"), filepath.Join(stage, "artwork"), journal.HadArtwork},
		{filepath.Join(target, ".playback-segment-dir"), filepath.Join(target, ".flixr-restore-prior-segment-dir"), filepath.Join(stage, ".playback-segment-dir"), journal.HadSidecar},
	}
	for index, move := range moves {
		if move.existed {
			if err := os.Rename(move.live, move.prior); err != nil {
				return errors.New("existing installation could not be staged for replacement")
			}
		}
		if _, err := os.Stat(move.staged); errors.Is(err, os.ErrNotExist) && filepath.Base(move.staged) == "artwork" {
			continue
		}
		if err := os.Rename(move.staged, move.live); err != nil {
			return errors.New("restored installation could not be published")
		}
		if interruptAfter == index+1 {
			return errSimulatedRestoreInterruption
		}
	}
	return syncDirectory(target)
}

func RecoverInterruptedRestore(target string) error {
	if !filepath.IsAbs(target) {
		return errors.New("restore target must be an absolute path")
	}
	target = filepath.Clean(target)
	targetInfo, err := os.Lstat(target)
	if err != nil || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("restore target must be an existing directory, not a symlink")
	}
	journalPath := filepath.Join(target, restoreJournalName)
	journalInfo, err := os.Lstat(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !journalInfo.Mode().IsRegular() || journalInfo.Size() > maxRestoreJournalBytes {
		return errors.New("restore journal is invalid")
	}
	data, err := os.ReadFile(journalPath)
	if err != nil {
		return errors.New("restore journal cannot be read")
	}
	var journal restoreJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil || (journal.Phase != "prepared" && journal.Phase != "published") || !validRestoreStageName(journal.Stage) {
		return errors.New("restore journal is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("restore journal is invalid")
	}
	stage := filepath.Join(target, journal.Stage)
	stageInfo, err := os.Lstat(stage)
	if err != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("restore journal stage is invalid")
	}
	items := []struct {
		live, prior string
		existed     bool
	}{
		{filepath.Join(target, "flixr.db"), filepath.Join(target, ".flixr-restore-prior.db"), journal.HadDatabase},
		{filepath.Join(target, "artwork"), filepath.Join(target, ".flixr-restore-prior-artwork"), journal.HadArtwork},
		{filepath.Join(target, ".playback-segment-dir"), filepath.Join(target, ".flixr-restore-prior-segment-dir"), journal.HadSidecar},
	}
	if journal.Phase == "prepared" {
		for _, item := range items {
			if item.existed {
				if _, priorErr := os.Lstat(item.prior); errors.Is(priorErr, os.ErrNotExist) {
					continue
				}
				_ = os.RemoveAll(item.live)
				if err := os.Rename(item.prior, item.live); err != nil {
					return errors.New("interrupted restore could not recover the prior installation")
				}
			} else {
				staged := filepath.Join(stage, filepath.Base(item.live))
				if filepath.Base(item.live) == "artwork" {
					staged = filepath.Join(stage, "artwork")
				}
				if _, stageErr := os.Lstat(staged); errors.Is(stageErr, os.ErrNotExist) {
					_ = os.RemoveAll(item.live)
				}
				_ = os.RemoveAll(item.prior)
			}
		}
	} else {
		for _, item := range items {
			_ = os.RemoveAll(item.prior)
		}
	}
	_ = os.RemoveAll(stage)
	if err := os.Remove(journalPath); err != nil {
		return errors.New("restore journal could not be removed")
	}
	return syncDirectory(target)
}

func validRestoreStageName(name string) bool {
	if len(name) != len(restoreStagePrefix)+12 || name[:len(restoreStagePrefix)] != restoreStagePrefix {
		return false
	}
	suffix := name[len(restoreStagePrefix):]
	decoded, err := hex.DecodeString(suffix)
	return err == nil && hex.EncodeToString(decoded) == suffix
}

func regularFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func directoryExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
