package config_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/config"
)

func TestLoadParsesDemoFlag(t *testing.T) {
	t.Setenv("FLIXR_DEMO", "true")
	bootstrap, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrap.Demo {
		t.Fatal("FLIXR_DEMO=true did not enable demo")
	}
	t.Setenv("FLIXR_DEMO", "not-a-bool")
	if _, err := config.Load(); err == nil {
		t.Fatal("invalid FLIXR_DEMO was accepted")
	}
}
