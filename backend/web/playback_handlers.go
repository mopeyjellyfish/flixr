package web

import (
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/playback"
)

type playbackPlanRequest struct {
	CatalogID    string                      `json:"catalog_id"`
	Capabilities playback.ClientCapabilities `json:"capabilities"`
}

type playbackPositionRequest struct {
	PositionMS int64 `json:"position_ms"`
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
	profile, _ := s.house.Profile(s.session(r))
	position, _ := s.house.Position(s.session(r), item.ID)
	session, err := s.playback.Create(profile.ID, item.ID, plan, position)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	write(w, http.StatusCreated, playbackResponse(session))
}

func playbackResponse(session playback.Session) map[string]any {
	base := "/api/v1/playback/sessions/" + session.ID
	mediaURL := base + "/media"
	if session.Plan.Kind == playback.Remux || session.Plan.Kind == playback.Transcode {
		mediaURL = base + "/manifest.m3u8"
	}
	return map[string]any{
		"plan":             session.Plan,
		"session_id":       session.ID,
		"media_url":        mediaURL,
		"heartbeat_url":    base + "/heartbeat",
		"seek_url":         base + "/seek",
		"stop_url":         base + "/stop",
		"resume_ms":        session.PositionMS,
		"stream_offset_ms": session.StreamOffsetMS,
		"expires_at":       session.ExpiresAt.Unix(),
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
	file, err := s.catalog.Open(session.CatalogID)
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
	if !decode(r, &body) || body.PositionMS < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	updated, err := s.playback.Heartbeat(session.ID, session.ProfileID, body.PositionMS)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	if err := s.house.Progress(s.session(r), session.CatalogID, body.PositionMS); err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
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
	if !decode(r, &body) || body.PositionMS < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	updated, err := s.playback.Seek(session.ID, session.ProfileID, body.PositionMS)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	if err := s.house.Progress(s.session(r), session.CatalogID, body.PositionMS); err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
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
	if err := s.house.Progress(s.session(r), session.CatalogID, session.PositionMS); err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
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
	catalogID, ok := s.playback.InputCatalog(r.PathValue("token"))
	if !ok {
		fail(w, http.StatusForbidden, "playback_input_invalid")
		return
	}
	file, err := s.catalog.Open(catalogID)
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
	for _, key := range []string{"playback.segment_dir", "playback.generation_bytes", "playback.global_bytes", "playback.max_generations"} {
		if s.settingsLocks[key] {
			fail(w, http.StatusConflict, "environment_locked")
			return
		}
	}
	settings := s.playback.Settings()
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
