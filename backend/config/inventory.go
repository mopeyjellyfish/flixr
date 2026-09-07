package config

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/mopeyjellyfish/flixr/backend/playback"
)

// SettingDefinition is the supported server-settings contract. Values are
// resolved as environment (including _FILE secrets), then persisted settings,
// then this default. Startup-only settings are intentionally read-only in UI.
type SettingDefinition struct {
	Key, Category, Scope, Default, Environment, FileSecret, Persistence, Restart, Valid string
	Secret, Advanced                                                                    bool
}

func Inventory(dataDir string) []SettingDefinition {
	defaults := playback.DefaultSettings(dataDir)
	return []SettingDefinition{
		{"server.data_dir", "Administration", "server", filepath.Join(".", "flixr-data"), "FLIXR_DATA_DIR", "", "environment", "restart", "writable directory", false, true},
		{"server.listen_addr", "Network", "server", "127.0.0.1:8787", "FLIXR_LISTEN_ADDR", "", "environment", "restart", "valid host:port", false, false},
		{"server.tls_cert", "Network", "server", "", "FLIXR_TLS_CERT", "", "environment", "restart", "readable certificate with matching key", false, true},
		{"server.tls_key", "Network", "server", "", "FLIXR_TLS_KEY", "", "environment", "restart", "readable key with matching certificate", true, true},
		{"server.demo", "Administration", "server", "false", "FLIXR_DEMO", "", "environment", "restart", "boolean", false, true},
		{"household.owner_password", "Household", "server", "", "FLIXR_OWNER_PASSWORD", "FLIXR_OWNER_PASSWORD_FILE", "startup only", "restart", "non-empty", true, true},
		{"household.initial_profile", "Household", "server", "", "FLIXR_INITIAL_PROFILE", "", "startup only", "restart", "valid profile name", false, true},
		{"library.films_root", "Libraries", "server", "", "FLIXR_FILMS_ROOT", "", "database", "immediate", "empty or existing directory", false, false},
		{"library.tv_root", "Libraries", "server", "", "FLIXR_TV_ROOT", "", "database", "immediate", "empty or existing directory", false, false},
		{"metadata.tmdb_token", "Metadata", "server", "", "FLIXR_TMDB_TOKEN", "FLIXR_TMDB_TOKEN_FILE", "database", "immediate", "provider token", true, false},
		{"background.scan_on_start", "Background work", "server", "false", "FLIXR_SCAN_ON_START", "", "environment", "restart", "boolean", false, true},
		{"background.scan_workers", "Background work", "server", "4", "FLIXR_SCAN_WORKERS", "", "environment", "restart", "integer 1–32", false, true},
		{"playback.segment_dir", "Playback", "server", defaults.SegmentDir, "FLIXR_SEGMENT_DIR", "", "sidecar and database", "restart", "absolute, Flixr-owned directory", false, false},
		{"playback.generation_bytes", "Playback", "server", strconv.FormatInt(defaults.GenerationBytes, 10), "FLIXR_GENERATION_BYTES", "", "database", "immediate", "positive; no more than global bytes", false, true},
		{"playback.global_bytes", "Playback", "server", strconv.FormatInt(defaults.GlobalBytes, 10), "FLIXR_GLOBAL_BYTES", "", "database", "immediate", "at least generation bytes", false, false},
		{"playback.max_generations", "Playback", "server", strconv.Itoa(defaults.MaxGenerations), "FLIXR_MAX_GENERATIONS", "", "database", "immediate", "positive; fits global byte limit", false, false},
		{"playback.lease_ttl", "Playback", "server", defaults.LeaseTTL.String(), "FLIXR_LEASE_TTL", "", "environment", "restart", "positive; over twice heartbeat", false, true},
		{"playback.heartbeat_interval", "Playback", "server", defaults.HeartbeatInterval.String(), "FLIXR_HEARTBEAT_INTERVAL", "", "environment", "restart", "positive; under half lease TTL", false, true},
		{"playback.segment_window", "Playback", "server", defaults.SegmentWindow.String(), "FLIXR_SEGMENT_WINDOW", "", "environment", "restart", "positive duration", false, true},
		{"playback.process_grace", "Playback", "server", defaults.ProcessGrace.String(), "FLIXR_PROCESS_GRACE", "", "environment", "restart", "positive duration", false, true},
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
