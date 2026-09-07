package config_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/config"
)

func TestInventoryListsSupportedSettingsAndSecretFileMappings(t *testing.T) {
	settings := config.Inventory("/var/lib/flixr")
	byKey := map[string]config.SettingDefinition{}
	for _, setting := range settings {
		byKey[setting.Key] = setting
	}
	for _, key := range []string{"library.films_root", "metadata.tmdb_token", "playback.global_bytes", "server.listen_addr"} {
		if _, ok := byKey[key]; !ok {
			t.Fatalf("inventory missing %s", key)
		}
	}
	if got := byKey["metadata.tmdb_token"]; !got.Secret || got.FileSecret != "FLIXR_TMDB_TOKEN_FILE" || got.Environment != "FLIXR_TMDB_TOKEN" {
		t.Fatalf("TMDB definition = %+v", got)
	}
	if byKey["playback.global_bytes"].Default != "536870912" {
		t.Fatal("playback default missing")
	}
}

func TestBootstrapLocksOnlyExplicitEnvironmentValues(t *testing.T) {
	t.Setenv("FLIXR_GLOBAL_BYTES", "1024")
	t.Setenv("FLIXR_TMDB_TOKEN_FILE", "/tmp/token")
	locks := config.EnvironmentLocks()
	if !locks["playback.global_bytes"] || !locks["metadata.tmdb_token"] || locks["library.films_root"] {
		t.Fatalf("locks = %#v", locks)
	}
}
