package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/config"
	"github.com/mopeyjellyfish/flixr/backend/playback"
)

type effectiveSetting struct {
	config.SettingDefinition
	Value   string `json:"value"`
	Source  string `json:"source"`
	Mutable bool   `json:"mutable"`
}

func (s *Server) settingsInventory(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	write(w, http.StatusOK, map[string]any{"settings": s.effectiveSettings()})
}

func (s *Server) effectiveSettings() []effectiveSetting {
	films, tv := s.catalog.Roots()
	p := s.playback.Settings()
	values := map[string]string{
		"library.films_root": films, "library.tv_root": tv, "metadata.tmdb_token": configured(s.catalog.TMDBConfigured()),
		"playback.segment_dir": p.SegmentDir, "playback.generation_bytes": strconv.FormatInt(p.GenerationBytes, 10),
		"playback.global_bytes": strconv.FormatInt(p.GlobalBytes, 10), "playback.max_generations": strconv.Itoa(p.MaxGenerations),
		"playback.lease_ttl": p.LeaseTTL.String(), "playback.heartbeat_interval": p.HeartbeatInterval.String(),
		"playback.segment_window": p.SegmentWindow.String(), "playback.process_grace": p.ProcessGrace.String(),
	}
	settings := make([]effectiveSetting, 0, len(config.Inventory("")))
	for _, definition := range config.Inventory("") {
		value := values[definition.Key]
		if configured, ok := s.settingsValues[definition.Key]; ok {
			value = configured
		}
		if value == "" {
			value = definition.Default
		}
		source := "default"
		if definition.Persistence == "database" || definition.Persistence == "sidecar and database" {
			source = "saved"
		}
		if s.settingsLocks[definition.Key] {
			source = "environment"
		}
		if definition.Secret {
			value = "configured"
			if !s.secretConfigured(definition.Key) {
				value = "not configured"
			}
			if s.settingsLocks[definition.Key] {
				value = "environment-managed"
			}
		}
		mutable := !definition.Secret && (definition.Key == "library.films_root" || definition.Key == "library.tv_root" || strings.HasPrefix(definition.Key, "playback.")) && !s.settingsLocks[definition.Key]
		settings = append(settings, effectiveSetting{SettingDefinition: definition, Value: value, Source: source, Mutable: mutable})
	}
	return settings
}

func configured(value bool) string {
	if value {
		return "configured"
	}
	return "not configured"
}
func (s *Server) secretConfigured(key string) bool {
	return key == "metadata.tmdb_token" && s.catalog.TMDBConfigured()
}

func (s *Server) settingsExport(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	values := map[string]string{}
	for _, setting := range s.effectiveSettings() {
		if !setting.Secret && setting.Persistence != "environment" {
			values[setting.Key] = setting.Value
		}
	}
	write(w, http.StatusOK, map[string]any{"version": 1, "settings": values})
}

func (s *Server) settingsImportPreview(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) || !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Version  int               `json:"version"`
		Settings map[string]string `json:"settings"`
	}
	if !decode(r, &body) || body.Version != 1 || body.Settings == nil {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	allowed := map[string]bool{"library.films_root": true, "library.tv_root": true, "playback.segment_dir": true, "playback.generation_bytes": true, "playback.global_bytes": true, "playback.max_generations": true}
	for key := range body.Settings {
		if !allowed[key] {
			fail(w, http.StatusConflict, "import_requires_review")
			return
		}
	}
	changes := make(map[string]map[string]string)
	for _, setting := range s.effectiveSettings() {
		value, supplied := body.Settings[setting.Key]
		if !supplied {
			continue
		}
		if s.settingsLocks[setting.Key] {
			fail(w, http.StatusConflict, "environment_locked")
			return
		}
		if value != setting.Value {
			changes[setting.Key] = map[string]string{"from": setting.Value, "to": value}
		}
	}
	write(w, http.StatusOK, map[string]any{"changes": changes, "requires_review": false})
}

func (s *Server) settingsImport(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) || !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Version  int               `json:"version"`
		Settings map[string]string `json:"settings"`
	}
	if !decode(r, &body) || body.Version != 1 || len(body.Settings) == 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	scope := ""
	current := map[string]string{}
	for _, setting := range s.effectiveSettings() {
		current[setting.Key] = setting.Value
	}
	for key := range body.Settings {
		if body.Settings[key] == current[key] {
			continue
		}
		next := ""
		if key == "library.films_root" || key == "library.tv_root" {
			next = "library"
		}
		if key == "playback.segment_dir" || key == "playback.generation_bytes" || key == "playback.global_bytes" || key == "playback.max_generations" {
			next = "playback"
		}
		if next == "" {
			fail(w, http.StatusConflict, "import_requires_review")
			return
		}
		if s.settingsLocks[key] {
			fail(w, http.StatusConflict, "environment_locked")
			return
		}
		if scope != "" && scope != next {
			fail(w, http.StatusConflict, "import_requires_review")
			return
		}
		scope = next
	}
	if scope == "" {
		write(w, http.StatusOK, map[string]bool{"imported": true})
		return
	}
	if scope == "library" {
		films, tv := s.catalog.Roots()
		if value, ok := body.Settings["library.films_root"]; ok {
			films = value
		}
		if value, ok := body.Settings["library.tv_root"]; ok {
			tv = value
		}
		if err := s.catalog.SetRoots(films, tv); err != nil {
			fail(w, http.StatusBadRequest, "invalid_roots")
			return
		}
		write(w, http.StatusOK, map[string]bool{"imported": true})
		return
	}
	settings := s.playback.Settings()
	var err error
	if value, ok := body.Settings["playback.segment_dir"]; ok {
		settings.SegmentDir = value
	}
	if value, ok := body.Settings["playback.generation_bytes"]; ok {
		settings.GenerationBytes, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_playback_settings")
			return
		}
	}
	if value, ok := body.Settings["playback.global_bytes"]; ok {
		settings.GlobalBytes, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_playback_settings")
			return
		}
	}
	if value, ok := body.Settings["playback.max_generations"]; ok {
		settings.MaxGenerations, err = strconv.Atoi(value)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_playback_settings")
			return
		}
	}
	if err := s.playback.UpdateSettings(settings); err != nil {
		if errors.Is(err, playback.ErrRestartRequired) {
			write(w, http.StatusAccepted, map[string]any{"imported": true, "restart_required": true})
			return
		}
		if errors.Is(err, playback.ErrInvalidSettings) {
			fail(w, http.StatusBadRequest, "invalid_playback_settings")
			return
		}
		if errors.Is(err, playback.ErrCapacity) {
			fail(w, http.StatusConflict, "playback_active")
			return
		}
		fail(w, http.StatusInternalServerError, "playback_settings_failed")
		return
	}
	write(w, http.StatusOK, map[string]bool{"imported": true})
}
