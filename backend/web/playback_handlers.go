package web

import (
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
)

type playbackPlanRequest struct {
	CatalogID    string                      `json:"catalog_id"`
	Capabilities playback.ClientCapabilities `json:"capabilities"`
}

type playbackPositionRequest struct {
	PositionMS  int64 `json:"position_ms"`
	Observation int64 `json:"observation"`
	ObservedAt  int64 `json:"observed_at"`
	Ended       bool  `json:"ended"`
}

type playbackSettingsRequest struct {
	SegmentDir      string `json:"segment_dir"`
	GenerationBytes int64  `json:"generation_bytes"`
	GlobalBytes     int64  `json:"global_bytes"`
	MaxGenerations  int    `json:"max_generations"`
}

func mediaProperties(item catalog.Item) playback.MediaProperties {
	audio := ""
	if len(item.Audio) > 0 {
		audio = item.Audio[0].Codec
	}
	subtitles := make([]string, 0, len(item.Subtitles))
	for _, track := range item.Subtitles {
		subtitles = append(subtitles, track.Codec)
	}
	return playback.MediaProperties{Container: item.Container, VideoCodec: item.VideoCodec, VideoProfile: item.VideoProfile, AudioCodec: audio, Subtitles: subtitles}
}

func (s *Server) playbackPlan(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.profile(w, r) {
		return
	}
	var body playbackPlanRequest
	if !decode(r, &body) || body.CatalogID == "" {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, err := s.catalog.PlaybackItem(body.CatalogID)
	if errors.Is(err, catalog.ErrCatalogNotFound) || (err == nil && (item.Kind != "film" && item.Kind != "episode")) {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	if errors.Is(err, catalog.ErrNotPlayable) {
		fail(w, http.StatusConflict, "playback_not_playable")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "playback_failed")
		return
	}
	s.readyMu.RLock()
	readiness := playback.ServerReadiness{FFmpeg: s.readiness.FFmpeg}
	s.readyMu.RUnlock()
	plan, err := playback.PlanFor(mediaProperties(item), body.Capabilities, readiness)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	plan.SourceKey = item.SourceKey()
	profile, _ := s.house.Profile(s.session(r))
	position, generation, err := s.house.ProgressState(s.session(r), item.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
	}
	session, err := s.playback.Create(profile.ID, item.ID, plan, position, generation+1)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	if r.Context().Err() != nil {
		s.playback.Stop(session.ID, profile.ID)
		return
	}
	// Only an admitted session may claim progress. A manual action or another
	// admitted plan during preparation wins the compare-and-swap and revokes
	// this candidate before its unguessable session ID is exposed to the client.
	if _, _, err := s.house.BeginPlayback(profile.ID, item.ID, generation); err != nil {
		s.playback.Stop(session.ID, profile.ID)
		if errors.Is(err, household.ErrProgressConflict) {
			fail(w, http.StatusConflict, "progress_conflict")
		} else {
			fail(w, http.StatusInternalServerError, "progress_failed")
		}
		return
	}
	write(w, http.StatusCreated, playbackResponse(session))
}

func completionEvent(catalogID string, item catalog.Item, completed bool) *household.ViewingEvent {
	if !completed || item.ID == "" {
		return nil
	}
	return &household.ViewingEvent{CatalogID: catalogID, Title: item.Title, Kind: item.Kind, Type: household.EventCompleted, Provenance: household.ProvenanceLocal}
}

func playbackResponse(session playback.Session) map[string]any {
	base := "/api/v1/playback/sessions/" + session.ID
	mediaURL := base + "/media"
	if session.Plan.Kind == playback.Remux || session.Plan.Kind == playback.Transcode {
		mediaURL = base + "/manifest.m3u8"
	}
	return map[string]any{
		"plan":                session.Plan,
		"session_id":          session.ID,
		"media_url":           mediaURL,
		"heartbeat_url":       base + "/heartbeat",
		"seek_url":            base + "/seek",
		"stop_url":            base + "/stop",
		"progress_generation": session.ProgressGeneration,
		"resume_ms":           session.PositionMS,
		"stream_offset_ms":    session.StreamOffsetMS,
		"expires_at":          session.ExpiresAt.Unix(),
	}
}

func playbackFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playback.ErrUnsupported):
		fail(w, http.StatusUnprocessableEntity, "playback_unsupported")
	case errors.Is(err, playback.ErrFFmpegUnavailable):
		fail(w, http.StatusServiceUnavailable, "ffmpeg_unavailable")
	case errors.Is(err, playback.ErrPreparing):
		fail(w, http.StatusServiceUnavailable, "playback_preparing")
	case errors.Is(err, playback.ErrCapacity):
		fail(w, http.StatusServiceUnavailable, "playback_capacity")
	case errors.Is(err, playback.ErrSessionInvalid):
		fail(w, http.StatusForbidden, "playback_session_invalid")
	default:
		fail(w, http.StatusInternalServerError, "playback_failed")
	}
}

func (s *Server) playbackSession(w http.ResponseWriter, r *http.Request, touch bool) (playback.Session, bool) {
	if !s.profile(w, r) {
		return playback.Session{}, false
	}
	profile, _ := s.house.Profile(s.session(r))
	session, ok := s.playback.Lookup(r.PathValue("id"), profile.ID, touch)
	if !ok {
		fail(w, http.StatusForbidden, "playback_session_invalid")
		return playback.Session{}, false
	}
	return session, true
}

func (s *Server) playbackMedia(w http.ResponseWriter, r *http.Request) {
	session, ok := s.playbackSession(w, r, true)
	if !ok {
		return
	}
	if session.Plan.Kind != playback.Direct {
		fail(w, http.StatusConflict, "playback_not_direct")
		return
	}
	file, err := s.catalog.OpenSource(session.CatalogID, session.Plan.SourceKey)
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	http.ServeContent(w, r, session.CatalogID, info.ModTime(), file)
}

func (s *Server) playbackManifest(w http.ResponseWriter, r *http.Request) {
	s.servePlaybackAsset(w, r, "index.m3u8")
}

func (s *Server) playbackSegment(w http.ResponseWriter, r *http.Request) {
	s.servePlaybackAsset(w, r, r.PathValue("name"))
}

func (s *Server) servePlaybackAsset(w http.ResponseWriter, r *http.Request, name string) {
	session, ok := s.playbackSession(w, r, true)
	if !ok {
		return
	}
	if session.Plan.Kind == playback.Direct {
		fail(w, http.StatusConflict, "playback_not_hls")
		return
	}
	file, err := s.playback.OpenAsset(session.ID, session.ProfileID, name)
	if err != nil {
		fail(w, http.StatusNotFound, "playback_asset_not_found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fail(w, http.StatusNotFound, "playback_asset_not_found")
		return
	}
	switch filepath.Ext(name) {
	case ".m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	case ".mp4", ".m4s":
		w.Header().Set("Content-Type", "video/mp4")
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (s *Server) playbackHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	var body playbackPositionRequest
	if !decode(r, &body) || body.PositionMS < 0 || body.Observation < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, itemErr := s.catalog.PlaybackItem(session.CatalogID)
	completed := body.Ended || (itemErr == nil && item.DurationMS > 0 && body.PositionMS >= item.DurationMS-item.DurationMS/10)
	accepted, err := s.house.RecordPlaybackProgress(session.ProfileID, session.CatalogID, body.PositionMS, session.ProgressGeneration, body.Observation, completed, completionEvent(session.CatalogID, item, completed))
	if err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
	}
	updated := session
	if accepted {
		updated, err = s.playback.Heartbeat(session.ID, session.ProfileID, body.PositionMS)
		if err != nil {
			playbackFailure(w, err)
			return
		}
	}

	write(w, http.StatusOK, map[string]any{"expires_at": updated.ExpiresAt.Unix()})
}

func (s *Server) playbackSeek(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	var body playbackPositionRequest
	if !decode(r, &body) || body.PositionMS < 0 || body.Observation < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, itemErr := s.catalog.PlaybackItem(session.CatalogID)
	completed := body.Ended || (itemErr == nil && item.DurationMS > 0 && body.PositionMS >= item.DurationMS-item.DurationMS/10)
	accepted, err := s.house.RecordPlaybackProgress(session.ProfileID, session.CatalogID, body.PositionMS, session.ProgressGeneration, body.Observation, completed, completionEvent(session.CatalogID, item, completed))
	if err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
	}
	updated := session
	if accepted {
		updated, err = s.playback.Seek(session.ID, session.ProfileID, body.PositionMS)
		if err != nil {
			playbackFailure(w, err)
			return
		}
	}

	write(w, http.StatusOK, playbackResponse(updated))
}

func (s *Server) playbackStop(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	if !s.playback.Stop(session.ID, session.ProfileID) {
		fail(w, http.StatusForbidden, "playback_session_invalid")
		return
	}
	write(w, http.StatusOK, map[string]bool{"stopped": true})
}

func (s *Server) playbackInput(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		fail(w, http.StatusForbidden, "playback_input_forbidden")
		return
	}
	catalogID, sourceKey, ok := s.playback.InputSource(r.PathValue("token"))
	if !ok {
		fail(w, http.StatusForbidden, "playback_input_invalid")
		return
	}
	file, err := s.catalog.OpenSource(catalogID, sourceKey)
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	http.ServeContent(w, r, catalogID, info.ModTime(), file)
}

func (s *Server) playbackSettings(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		write(w, http.StatusOK, s.playback.Settings())
		return
	}
	if !s.sameOrigin(w, r) {
		return
	}
	var body playbackSettingsRequest
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	settings := s.playback.Settings()
	requested := map[string]string{"playback.segment_dir": body.SegmentDir, "playback.generation_bytes": strconv.FormatInt(body.GenerationBytes, 10), "playback.global_bytes": strconv.FormatInt(body.GlobalBytes, 10), "playback.max_generations": strconv.Itoa(body.MaxGenerations)}
	current := map[string]string{"playback.segment_dir": settings.SegmentDir, "playback.generation_bytes": strconv.FormatInt(settings.GenerationBytes, 10), "playback.global_bytes": strconv.FormatInt(settings.GlobalBytes, 10), "playback.max_generations": strconv.Itoa(settings.MaxGenerations)}
	for key, value := range requested {
		if s.settingsLocks[key] && value != current[key] {
			fail(w, http.StatusConflict, "environment_locked")
			return
		}
	}
	settings.SegmentDir = body.SegmentDir
	settings.GenerationBytes = body.GenerationBytes
	settings.GlobalBytes = body.GlobalBytes
	settings.MaxGenerations = body.MaxGenerations
	if err := s.playback.UpdateSettings(settings); err != nil {
		switch {
		case errors.Is(err, playback.ErrInvalidSettings):
			fail(w, http.StatusBadRequest, "invalid_playback_settings")
		case errors.Is(err, playback.ErrRestartRequired):
			write(w, http.StatusAccepted, map[string]any{"settings": settings, "restart_required": true})
		case errors.Is(err, playback.ErrCapacity):
			fail(w, http.StatusConflict, "playback_active")
		default:
			fail(w, http.StatusInternalServerError, "playback_settings_failed")
		}
		return
	}
	write(w, http.StatusOK, map[string]any{"settings": settings, "restart_required": false})
}

func (s *Server) playbackStatus(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	write(w, http.StatusOK, s.playback.Status())
}
