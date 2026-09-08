package config_test

import (
	"github.com/mopeyjellyfish/flixr/backend/config"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentOverridesSavedPlaybackAndReadsSecretFile(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "password")
	if err := os.WriteFile(secret, []byte("private-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLIXR_OWNER_PASSWORD_FILE", secret)
	t.Setenv("FLIXR_FILMS_ROOT", "/media/films")
	t.Setenv("FLIXR_GLOBAL_BYTES", "1073741824")
	t.Setenv("FLIXR_MAX_GENERATIONS", "4")
	t.Setenv("FLIXR_SEGMENT_DIR", filepath.Join(dir, "cache"))
	t.Setenv("FLIXR_SEGMENT_WINDOW", "90s")
	t.Setenv("FLIXR_SCAN_ON_START", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	saved := playback.DefaultSettings(dir)
	got, err := cfg.PlaybackSettings(saved)
	if err != nil || got.MaxGenerations != 4 || got.GenerationBytes != saved.GenerationBytes || got.SegmentWindow != 90*time.Second || got.SegmentDir != filepath.Join(dir, "cache") {
		t.Fatalf("settings %+v, %v", got, err)
	}
	if cfg.OwnerPassword != "private-password" || cfg.FilmsRoot == nil || *cfg.FilmsRoot != "/media/films" || !cfg.ScanOnStart {
		t.Fatal("environment was not applied")
	}
	t.Setenv("FLIXR_OWNER_PASSWORD", "do-not-print-me")
	_, err = config.Load()
	if err == nil || strings.Contains(err.Error(), "do-not-print-me") {
		t.Fatal("conflicting secret input not safely rejected")
	}
}

func TestEnvironmentRejectsInvalidResourceLimits(t *testing.T) {
	for _, pair := range [][2]string{{"FLIXR_GLOBAL_BYTES", "0"}, {"FLIXR_MAX_GENERATIONS", "999"}, {"FLIXR_LEASE_TTL", "1s"}, {"FLIXR_SEGMENT_WINDOW", "invalid"}, {"FLIXR_SCAN_WORKERS", "0"}, {"FLIXR_SEGMENT_DIR", "relative"}, {"FLIXR_METADATA_ENABLED", "offline-ish"}} {
		t.Run(pair[0], func(t *testing.T) {
			t.Setenv(pair[0], pair[1])
			cfg, err := config.Load()
			if err == nil {
				_, err = cfg.PlaybackSettings(playback.DefaultSettings(t.TempDir()))
			}
			if err == nil {
				t.Fatal("invalid environment accepted")
			}
		})
	}
}

func TestEnvironmentDistinguishesOmittedAndDisabledMetadata(t *testing.T) {
	cfg, err := config.Load()
	if err != nil || cfg.MetadataEnabled != nil {
		t.Fatalf("omitted metadata enabled = %v, %v", cfg.MetadataEnabled, err)
	}
	t.Setenv("FLIXR_METADATA_ENABLED", "false")
	cfg, err = config.Load()
	if err != nil || cfg.MetadataEnabled == nil || *cfg.MetadataEnabled {
		t.Fatalf("disabled metadata enabled = %v, %v", cfg.MetadataEnabled, err)
	}
}
