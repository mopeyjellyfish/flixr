package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// SettingDefinition is the supported server-settings contract. Values are
// resolved as environment (including _FILE secrets), then persisted settings,
// then this default. Startup-only settings are intentionally read-only in UI.
type SettingDefinition struct {
	Key, Category, Scope, Default, Environment, FileSecret, Persistence, Restart string
	Secret, Advanced                                                             bool
}

func Inventory(dataDir string) []SettingDefinition {
	return []SettingDefinition{
		{"server.data_dir", "Administration", "server", filepath.Join(".", "flixr-data"), "FLIXR_DATA_DIR", "", "environment", "restart", false, true},
		{"server.listen_addr", "Network", "server", "127.0.0.1:8787", "FLIXR_LISTEN_ADDR", "", "environment", "restart", false, false},
		{"server.tls_cert", "Network", "server", "", "FLIXR_TLS_CERT", "", "environment", "restart", false, true},
		{"server.tls_key", "Network", "server", "", "FLIXR_TLS_KEY", "", "environment", "restart", true, true},
		{"server.demo", "Administration", "server", "false", "FLIXR_DEMO", "", "environment", "restart", false, true},
		{"household.owner_password", "Household", "server", "", "FLIXR_OWNER_PASSWORD", "FLIXR_OWNER_PASSWORD_FILE", "startup only", "restart", true, true},
		{"household.initial_profile", "Household", "server", "", "FLIXR_INITIAL_PROFILE", "", "startup only", "restart", false, true},
		{"library.films_root", "Libraries", "server", "", "FLIXR_FILMS_ROOT", "", "database", "immediate", false, false},
		{"library.tv_root", "Libraries", "server", "", "FLIXR_TV_ROOT", "", "database", "immediate", false, false},
		{"metadata.tmdb_token", "Metadata", "server", "", "FLIXR_TMDB_TOKEN", "FLIXR_TMDB_TOKEN_FILE", "database", "immediate", true, false},
		{"background.scan_on_start", "Background work", "server", "false", "FLIXR_SCAN_ON_START", "", "environment", "restart", false, true},
		{"background.scan_workers", "Background work", "server", "4", "FLIXR_SCAN_WORKERS", "", "environment", "restart", false, true},
		{"playback.segment_dir", "Playback", "server", filepath.Join(dataDir, "segments"), "FLIXR_SEGMENT_DIR", "", "sidecar and database", "restart", false, false},
		{"playback.generation_bytes", "Playback", "server", strconv.FormatInt(256<<20, 10), "FLIXR_GENERATION_BYTES", "", "database", "immediate", false, true},
		{"playback.global_bytes", "Playback", "server", strconv.FormatInt(512<<20, 10), "FLIXR_GLOBAL_BYTES", "", "database", "immediate", false, false},
		{"playback.max_generations", "Playback", "server", "2", "FLIXR_MAX_GENERATIONS", "", "database", "immediate", false, false},
		{"playback.lease_ttl", "Playback", "server", (45 * time.Second).String(), "FLIXR_LEASE_TTL", "", "environment", "restart", false, true},
		{"playback.heartbeat_interval", "Playback", "server", (15 * time.Second).String(), "FLIXR_HEARTBEAT_INTERVAL", "", "environment", "restart", false, true},
		{"playback.segment_window", "Playback", "server", (60 * time.Second).String(), "FLIXR_SEGMENT_WINDOW", "", "environment", "restart", false, true},
		{"playback.process_grace", "Playback", "server", (2 * time.Second).String(), "FLIXR_PROCESS_GRACE", "", "environment", "restart", false, true},
	}
}

// EnvironmentLocks records only explicit startup overrides. It never reads a
// secret value, so this data is safe for the owner settings response and logs.
func EnvironmentLocks() map[string]bool {
	locks := make(map[string]bool)
	for _, definition := range Inventory("") {
		if _, ok := os.LookupEnv(definition.Environment); ok && definition.Environment != "" {
			locks[definition.Key] = true
			continue
		}
		if definition.FileSecret != "" {
			if _, ok := os.LookupEnv(definition.FileSecret); ok {
				locks[definition.Key] = true
			}
		}
	}
	return locks
}

// NonSecretValues exposes startup values suitable for the authenticated owner
// configuration view. Credentials deliberately have no entry here.
func (b Bootstrap) NonSecretValues() map[string]string {
	values := map[string]string{
		"server.data_dir": b.DataDir, "server.listen_addr": b.ListenAddr, "server.tls_cert": b.TLSCert,
		"server.demo": strconv.FormatBool(b.Demo), "household.initial_profile": b.InitialProfile,
		"background.scan_on_start": strconv.FormatBool(b.ScanOnStart), "background.scan_workers": strconv.Itoa(b.ScanWorkers),
	}
	if b.SegmentDir != "" {
		values["playback.segment_dir"] = b.SegmentDir
	}
	for key, value := range b.Playback {
		for setting, environment := range map[string]string{
			"playback.generation_bytes": "FLIXR_GENERATION_BYTES", "playback.global_bytes": "FLIXR_GLOBAL_BYTES", "playback.max_generations": "FLIXR_MAX_GENERATIONS",
			"playback.lease_ttl": "FLIXR_LEASE_TTL", "playback.heartbeat_interval": "FLIXR_HEARTBEAT_INTERVAL", "playback.segment_window": "FLIXR_SEGMENT_WINDOW", "playback.process_grace": "FLIXR_PROCESS_GRACE",
		} {
			if key == environment {
				values[setting] = value
			}
		}
	}
	return values
}
