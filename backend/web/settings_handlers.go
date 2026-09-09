package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/config"
	"github.com/mopeyjellyfish/flixr/backend/playback"
)

type effectiveSetting struct {
	config.SettingDefinition
	Value         string `json:"value"`
	Source        string `json:"source"`
	PendingValue  string `json:"pending_value,omitempty"`
	PendingSource string `json:"pending_source,omitempty"`
	Mutable       bool   `json:"mutable"`
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
		"library.films_root": films, "library.tv_root": tv, "metadata.enabled": strconv.FormatBool(s.catalog.MetadataEnabled()), "metadata.tmdb_token": configured(s.catalog.TMDBOverrideConfigured()),
		"playback.segment_dir": p.SegmentDir, "playback.generation_bytes": strconv.FormatInt(p.GenerationBytes, 10),
		"playback.global_bytes": strconv.FormatInt(p.GlobalBytes, 10), "playback.max_generations": strconv.Itoa(p.MaxGenerations),
		"playback.lease_ttl": p.LeaseTTL.String(), "playback.heartbeat_interval": p.HeartbeatInterval.String(),
		"playback.segment_window": p.SegmentWindow.String(), "playback.process_grace": p.ProcessGrace.String(),
	}
	settings := make([]effectiveSetting, 0, len(config.Inventory("")))
	for _, definition := range config.Inventory("") {
		value := values[definition.Key]
		pendingValue, pendingSource := "", ""
		configuredValue, configured := s.settingsValues[definition.Key]
		pendingRoot := s.settingsLocks[definition.Key] && (definition.Key == "library.films_root" || definition.Key == "library.tv_root") && configured && configuredValue != value
		if configured && !pendingRoot {
			value = configuredValue
		} else if pendingRoot {
			pendingValue, pendingSource = configuredValue, "environment"
		}
		if value == "" {
			value = definition.Default
		}
		source := "default"
		if definition.Persistence == "database" || definition.Persistence == "sidecar and database" {
			source = "saved"
		}
		if s.settingsLocks[definition.Key] && !pendingRoot {
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
		mutable := !definition.Secret && writableOwnerSetting(definition.Key) && !s.settingsLocks[definition.Key]
		settings = append(settings, effectiveSetting{SettingDefinition: definition, Value: value, Source: source, PendingValue: pendingValue, PendingSource: pendingSource, Mutable: mutable})
	}
	return settings
}

func writableOwnerSetting(key string) bool {
	switch key {
	case "library.films_root", "library.tv_root", "playback.segment_dir", "playback.generation_bytes", "playback.global_bytes", "playback.max_generations":
		return true
	}
	return false
}

func configured(value bool) string {
	if value {
		return "configured"
	}
	return "not configured"
}
func (s *Server) secretConfigured(key string) bool {
	return key == "metadata.tmdb_token" && s.catalog.TMDBOverrideConfigured()
}

func (s *Server) settingsExport(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	values := map[string]string{}
	for _, setting := range s.effectiveSettings() {
		if setting.Mutable {
			values[setting.Key] = setting.Value
		}
	}
	write(w, http.StatusOK, map[string]any{"version": 1, "settings": values})
}

type settingsImportRequest struct {
	Version  int               `json:"version"`
	Settings map[string]string `json:"settings"`
}
type settingsImportPlan struct {
	scope     string
	settings  playback.Settings
	films, tv string
	changes   map[string]map[string]string
}

func (s *Server) settingsImportPreview(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) || !s.sameOrigin(w, r) {
		return
	}
	var body settingsImportRequest
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	plan, code := s.planSettingsImport(body)
	if code != "" {
		status := http.StatusBadRequest
		if code == "environment_locked" || code == "import_requires_review" || code == "library_change_requires_preview" {
			status = http.StatusConflict
		}
		fail(w, status, code)
		return
	}
	write(w, http.StatusOK, map[string]any{"changes": plan.changes, "requires_review": false, "restart_required": plan.scope == "playback" && plan.settings.SegmentDir != s.playback.Settings().SegmentDir})
}

func (s *Server) settingsImport(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) || !s.sameOrigin(w, r) {
		return
	}
	var body settingsImportRequest
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	plan, code := s.planSettingsImport(body)
	if code != "" {
		status := http.StatusBadRequest
		if code == "environment_locked" || code == "import_requires_review" || code == "library_change_requires_preview" {
			status = http.StatusConflict
		}
		fail(w, status, code)
		return
	}
	if plan.scope == "" {
		write(w, http.StatusOK, map[string]bool{"imported": true})
		return
	}
	if plan.scope == "library" {
		if err := s.catalog.SetRoots(plan.films, plan.tv); err != nil {
			if errors.Is(err, catalog.ErrLocationChangeReviewRequired) {
				fail(w, http.StatusConflict, "library_change_requires_preview")
				return
			}
			fail(w, http.StatusBadRequest, "invalid_roots")
			return
		}
		write(w, http.StatusOK, map[string]bool{"imported": true})
		return
	}
	if err := s.playback.UpdateSettings(plan.settings); err != nil {
		if errors.Is(err, playback.ErrRestartRequired) {
			write(w, http.StatusAccepted, map[string]any{"imported": true, "restart_required": true})
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

func (s *Server) planSettingsImport(body settingsImportRequest) (settingsImportPlan, string) {
	if body.Version != 1 || len(body.Settings) == 0 {
		return settingsImportPlan{}, "invalid_request"
	}
	plan := settingsImportPlan{settings: s.playback.Settings(), changes: map[string]map[string]string{}}
	plan.films, plan.tv = s.catalog.Roots()
	current := map[string]string{}
	for _, setting := range s.effectiveSettings() {
		current[setting.Key] = setting.Value
	}
	for key, value := range body.Settings {
		scope := ""
		switch key {
		case "library.films_root", "library.tv_root":
			scope = "library"
		case "playback.segment_dir", "playback.generation_bytes", "playback.global_bytes", "playback.max_generations":
			scope = "playback"
		default:
			return settingsImportPlan{}, "import_requires_review"
		}
		if value == current[key] {
			continue
		}
		if s.settingsLocks[key] {
			return settingsImportPlan{}, "environment_locked"
		}
		if plan.scope != "" && plan.scope != scope {
			return settingsImportPlan{}, "import_requires_review"
		}
		plan.scope = scope
		plan.changes[key] = map[string]string{"from": current[key], "to": value}
		switch key {
		case "library.films_root":
			plan.films = value
		case "library.tv_root":
			plan.tv = value
		case "playback.segment_dir":
			plan.settings.SegmentDir = value
		case "playback.generation_bytes":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return settingsImportPlan{}, "invalid_playback_settings"
			}
			plan.settings.GenerationBytes = parsed
		case "playback.global_bytes":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return settingsImportPlan{}, "invalid_playback_settings"
			}
			plan.settings.GlobalBytes = parsed
		case "playback.max_generations":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return settingsImportPlan{}, "invalid_playback_settings"
			}
			plan.settings.MaxGenerations = parsed
		}
	}
	if plan.scope == "library" {
		if err := s.catalog.ValidateRoots(plan.films, plan.tv); err != nil {
			return settingsImportPlan{}, "invalid_roots"
		}
		requiresReview, err := s.catalog.RootChangesRequireReview(plan.films, plan.tv)
		if err != nil {
			return settingsImportPlan{}, "invalid_roots"
		}
		if requiresReview {
			return settingsImportPlan{}, "library_change_requires_preview"
		}
	}
	if plan.scope == "playback" {
		if err := plan.settings.Validate(); err != nil {
			return settingsImportPlan{}, "invalid_playback_settings"
		}
	}
	return plan, ""
}
