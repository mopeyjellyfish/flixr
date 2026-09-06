package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/playback"
)

// Environment is applied at startup; omitted values retain saved settings.
type Environment struct {
	FilmsRoot, TVRoot, TMDBToken              *string
	OwnerPassword, InitialProfile, SegmentDir string
	ScanOnStart                               bool
	ScanWorkers                               int
	Playback                                  map[string]string
}

func loadEnvironment() (Environment, error) {
	e := Environment{ScanWorkers: 4, Playback: map[string]string{}}
	for key, target := range map[string]**string{"FLIXR_FILMS_ROOT": &e.FilmsRoot, "FLIXR_TV_ROOT": &e.TVRoot} {
		if value, ok := os.LookupEnv(key); ok {
			*target = &value
		}
	}
	var err error
	e.TMDBToken, err = secret("FLIXR_TMDB_TOKEN")
	if err != nil {
		return e, err
	}
	password, err := secret("FLIXR_OWNER_PASSWORD")
	if err != nil {
		return e, err
	}
	if password != nil {
		if *password == "" {
			return e, errors.New("FLIXR_OWNER_PASSWORD must not be empty")
		}
		e.OwnerPassword = *password
	}
	e.InitialProfile = os.Getenv("FLIXR_INITIAL_PROFILE")
	e.SegmentDir = os.Getenv("FLIXR_SEGMENT_DIR")
	if e.SegmentDir != "" && !filepath.IsAbs(e.SegmentDir) {
		return e, errors.New("FLIXR_SEGMENT_DIR must be absolute")
	}
	if value := os.Getenv("FLIXR_SCAN_ON_START"); value != "" {
		e.ScanOnStart, err = strconv.ParseBool(value)
		if err != nil {
			return e, errors.New("FLIXR_SCAN_ON_START must be a boolean")
		}
	}
	if value := os.Getenv("FLIXR_SCAN_WORKERS"); value != "" {
		e.ScanWorkers, err = strconv.Atoi(value)
		if err != nil || e.ScanWorkers < 1 || e.ScanWorkers > 32 {
			return e, errors.New("FLIXR_SCAN_WORKERS must be between 1 and 32")
		}
	}
	for _, key := range []string{"FLIXR_GENERATION_BYTES", "FLIXR_GLOBAL_BYTES", "FLIXR_MAX_GENERATIONS", "FLIXR_LEASE_TTL", "FLIXR_HEARTBEAT_INTERVAL", "FLIXR_SEGMENT_WINDOW", "FLIXR_PROCESS_GRACE"} {
		if value, ok := os.LookupEnv(key); ok {
			e.Playback[key] = value
		}
	}
	return e, nil
}

func secret(key string) (*string, error) {
	value, direct := os.LookupEnv(key)
	path, file := os.LookupEnv(key + "_FILE")
	if direct && file {
		return nil, fmt.Errorf("set only one of %s and %s_FILE", key, key)
	}
	if file {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s_FILE", key)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s_FILE must be a regular file", key)
		}
		data, err := io.ReadAll(io.LimitReader(f, 16385))
		if err != nil || len(data) > 16384 {
			return nil, fmt.Errorf("invalid or oversized %s_FILE", key)
		}
		value = strings.TrimRight(string(data), "\r\n")
	}
	if !direct && !file {
		return nil, nil
	}
	return &value, nil
}

func (e Environment) PlaybackSettings(s playback.Settings) (playback.Settings, error) {
	if e.SegmentDir != "" {
		s.SegmentDir = filepath.Clean(e.SegmentDir)
	}
	for key, target := range map[string]*int64{"FLIXR_GENERATION_BYTES": &s.GenerationBytes, "FLIXR_GLOBAL_BYTES": &s.GlobalBytes} {
		if value, ok := e.Playback[key]; ok {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return s, fmt.Errorf("%s must be a byte count", key)
			}
			*target = parsed
		}
	}
	if value, ok := e.Playback["FLIXR_MAX_GENERATIONS"]; ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return s, errors.New("FLIXR_MAX_GENERATIONS must be an integer")
		}
		s.MaxGenerations = parsed
	}
	for key, target := range map[string]*time.Duration{"FLIXR_LEASE_TTL": &s.LeaseTTL, "FLIXR_HEARTBEAT_INTERVAL": &s.HeartbeatInterval, "FLIXR_SEGMENT_WINDOW": &s.SegmentWindow, "FLIXR_PROCESS_GRACE": &s.ProcessGrace} {
		if value, ok := e.Playback[key]; ok {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return s, fmt.Errorf("%s must be a duration such as 15s", key)
			}
			*target = parsed
		}
	}
	return s, s.Validate()
}
