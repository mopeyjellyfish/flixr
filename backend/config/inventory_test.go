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
	for _, key := range []string{"library.films_root", "metadata.enabled", "metadata.tmdb_token", "playback.global_bytes", "server.listen_addr"} {
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
	t.Setenv("FLIXR_METADATA_ENABLED", "false")
	locks := config.EnvironmentLocks()
	if !locks["playback.global_bytes"] || !locks["metadata.tmdb_token"] || !locks["metadata.enabled"] || locks["library.films_root"] {
		t.Fatalf("locks = %#v", locks)
	}
}

func TestBootstrapExposesNonSecretEnvironmentRootTargets(t *testing.T) {
	films, tv := "/media/new-films", ""
	values := (config.Bootstrap{Environment: config.Environment{FilmsRoot: &films, TVRoot: &tv}}).NonSecretValues()
	if values["library.films_root"] != films {
		t.Fatalf("films target = %q", values["library.films_root"])
	}
	if value, ok := values["library.tv_root"]; !ok || value != "" {
		t.Fatalf("explicit empty TV target = %q, present=%v", value, ok)
	}
}
