package playback

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

const (
	settingSegmentDir         = "playback_segment_dir"
	settingGenerationBytes    = "playback_generation_bytes"
	settingGlobalBytes        = "playback_global_bytes"
	settingMaxGenerations     = "playback_max_generations"
	segmentDirSidecar         = ".playback-segment-dir"
	defaultGenerationBytes    = int64(256 << 20)
	defaultGlobalBytes        = int64(512 << 20)
	defaultMaxGenerations     = 2
	defaultLeaseTTL           = 45 * time.Second
	defaultHeartbeatInterval  = 15 * time.Second
	defaultSegmentWindow      = 60 * time.Second
	defaultProcessGracePeriod = 2 * time.Second
	segmentDirMarker          = ".flixr-playback-segments"
	segmentDirMarkerContent   = "flixr playback segments v1\n"
)

// Settings are owner-managed playback resource bounds. Changes take effect only
// while no generation is active; a segment-directory change requires restart.
type Settings struct {
	SegmentDir        string        `json:"segment_dir"`
	GenerationBytes   int64         `json:"generation_bytes"`
	GlobalBytes       int64         `json:"global_bytes"`
	MaxGenerations    int           `json:"max_generations"`
	LeaseTTL          time.Duration `json:"-"`
	HeartbeatInterval time.Duration `json:"-"`
	SegmentWindow     time.Duration `json:"-"`
	ProcessGrace      time.Duration `json:"-"`
}

func DefaultSettings(dataDir string) Settings {
	return Settings{
		SegmentDir:        filepath.Join(dataDir, "segments"),
		GenerationBytes:   defaultGenerationBytes,
		GlobalBytes:       defaultGlobalBytes,
		MaxGenerations:    defaultMaxGenerations,
		LeaseTTL:          defaultLeaseTTL,
		HeartbeatInterval: defaultHeartbeatInterval,
		SegmentWindow:     defaultSegmentWindow,
		ProcessGrace:      defaultProcessGracePeriod,
	}
}

func (s Settings) Validate() error {
	if s.SegmentDir == "" || !filepath.IsAbs(s.SegmentDir) {
		return fmt.Errorf("%w: segment directory must be absolute", ErrInvalidSettings)
	}
	if s.GenerationBytes <= 0 || s.GlobalBytes < s.GenerationBytes {
		return fmt.Errorf("%w: byte limits must be positive and global must cover one generation", ErrInvalidSettings)
	}
	if s.MaxGenerations <= 0 || int64(s.MaxGenerations) > s.GlobalBytes/s.GenerationBytes {
		return fmt.Errorf("%w: generation concurrency exceeds the reserved global byte limit", ErrInvalidSettings)
	}
	if s.LeaseTTL <= 0 || s.HeartbeatInterval <= 0 || s.HeartbeatInterval*2 >= s.LeaseTTL {
		return fmt.Errorf("%w: heartbeat must be less than half the lease TTL", ErrInvalidSettings)
	}
	if s.SegmentWindow <= 0 || s.ProcessGrace <= 0 {
		return fmt.Errorf("%w: timing bounds must be positive", ErrInvalidSettings)
	}
	return nil
}

func validateSegmentDirOwnership(filesystem afero.Fs, dir string) error {
	entries, err := afero.ReadDir(filesystem, dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect playback segment directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() != segmentDirMarker {
			continue
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("%w: playback segment ownership marker is not a regular file", ErrInvalidSettings)
		}
		content, err := afero.ReadFile(filesystem, filepath.Join(dir, segmentDirMarker))
		if err != nil {
			return fmt.Errorf("read playback segment ownership marker: %w", err)
		}
		if string(content) != segmentDirMarkerContent {
			return fmt.Errorf("%w: playback segment ownership marker is invalid", ErrInvalidSettings)
		}
		return nil
	}
	for _, entry := range entries {
		if entry.Name() != ".flixr.lock" {
			return fmt.Errorf("%w: segment directory is not empty and is not owned by Flixr", ErrInvalidSettings)
		}
	}
	return nil
}

func claimSegmentDir(filesystem afero.Fs, dir string) error {
	if err := filesystem.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create segment directory: %w", err)
	}
	if err := validateSegmentDirOwnership(filesystem, dir); err != nil {
		return err
	}
	marker := filepath.Join(dir, segmentDirMarker)
	if _, err := filesystem.Stat(marker); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect playback segment ownership marker: %w", err)
	}
	if err := afero.WriteFile(filesystem, marker, []byte(segmentDirMarkerContent), 0o600); err != nil {
		return fmt.Errorf("write playback segment ownership marker: %w", err)
	}
	return nil
}

func LoadSettings(db *sqlite.DB, dataDir string) (Settings, error) {
	return LoadSettingsWithFilesystem(db, dataDir, afero.NewOsFs())
}

func LoadSettingsWithFilesystem(db *sqlite.DB, dataDir string, filesystem afero.Fs) (Settings, error) {
	settings := DefaultSettings(dataDir)
	if filesystem == nil {
		return Settings{}, errors.New("playback filesystem is nil")
	}
	if sidecar, err := LoadSegmentDir(filesystem, dataDir); err != nil {
		return Settings{}, err
	} else {
		settings.SegmentDir = sidecar
	}
	if db == nil {
		return settings, settings.Validate()
	}
	values := map[string]*string{}
	// SegmentDir comes from the sidecar because startup must lock it before SQLite opens.
	// SQLite retains a mirror for owner-readable settings and backup inspection.
	var generationBytes, globalBytes, maxGenerations string
	values[settingGenerationBytes] = &generationBytes
	values[settingGlobalBytes] = &globalBytes
	values[settingMaxGenerations] = &maxGenerations
	for key, target := range values {
		err := db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(target)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Settings{}, fmt.Errorf("load playback setting %s: %w", key, err)
		}
	}
	var err error
	if generationBytes != "" {
		settings.GenerationBytes, err = strconv.ParseInt(generationBytes, 10, 64)
		if err != nil {
			return Settings{}, fmt.Errorf("parse generation byte limit: %w", err)
		}
	}
	if globalBytes != "" {
		settings.GlobalBytes, err = strconv.ParseInt(globalBytes, 10, 64)
		if err != nil {
			return Settings{}, fmt.Errorf("parse global byte limit: %w", err)
		}
	}
	if maxGenerations != "" {
		settings.MaxGenerations, err = strconv.Atoi(maxGenerations)
		if err != nil {
			return Settings{}, fmt.Errorf("parse generation limit: %w", err)
		}
	}
	return settings, settings.Validate()
}

// LoadSegmentDir is safe to call before SQLite opens so a distinct segment
// directory can be locked before database access or startup reclamation.
func LoadSegmentDir(filesystem afero.Fs, dataDir string) (string, error) {
	path := filepath.Join(dataDir, segmentDirSidecar)
	value, err := afero.ReadFile(filesystem, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultSettings(dataDir).SegmentDir, nil
		}
		return "", fmt.Errorf("read playback segment directory: %w", err)
	}
	dir := strings.TrimSpace(string(value))
	if dir == "" || !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%w: persisted segment directory must be absolute", ErrInvalidSettings)
	}
	return filepath.Clean(dir), nil
}

func saveSettings(db *sqlite.DB, filesystem afero.Fs, settings Settings) error {
	if db == nil {
		return errors.New("playback settings are not durable")
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	temp, err := afero.TempFile(filesystem, db.DataDir(), ".playback-segment-dir-")
	if err != nil {
		return fmt.Errorf("stage segment directory setting: %w", err)
	}
	tempName := temp.Name()
	defer filesystem.Remove(tempName)
	if _, err := temp.Write([]byte(settings.SegmentDir + "\n")); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := filesystem.Chmod(tempName, 0o600); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := map[string]string{
		settingSegmentDir:      settings.SegmentDir,
		settingGenerationBytes: strconv.FormatInt(settings.GenerationBytes, 10),
		settingGlobalBytes:     strconv.FormatInt(settings.GlobalBytes, 10),
		settingMaxGenerations:  strconv.Itoa(settings.MaxGenerations),
	}
	for key, value := range values {
		if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := filesystem.Rename(tempName, filepath.Join(db.DataDir(), segmentDirSidecar)); err != nil {
		return fmt.Errorf("publish segment directory setting: %w", err)
	}
	return nil
}
