package web

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/captions"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
)

type playbackPlanRequest struct {
	CatalogID              string                                 `json:"catalog_id"`
	VersionID              string                                 `json:"version_id,omitempty"`
	ContinueWatchingIntent string                                 `json:"continue_watching_intent,omitempty"`
	Capabilities           playback.ClientCapabilities            `json:"capabilities"`
	VersionCapabilities    map[string]playback.ClientCapabilities `json:"version_capabilities,omitempty"`
	Quality                playback.QualityRequest                `json:"quality"`
	AudioStreamIndex       *int                                   `json:"audio_stream_index"`
	AudioExternal          bool                                   `json:"audio_external,omitempty"`
	SubtitleStreamIndex    *int                                   `json:"subtitle_stream_index"`
	SubtitleExternal       bool                                   `json:"subtitle_external,omitempty"`
}

type playbackAudioRequest struct {
	Capabilities     playback.ClientCapabilities `json:"capabilities"`
	AudioStreamIndex *int                        `json:"audio_stream_index"`
	AudioExternal    bool                        `json:"audio_external,omitempty"`
	PositionMS       int64                       `json:"position_ms"`
	Observation      int64                       `json:"observation"`
}

type playbackQualityRequest struct {
	Capabilities  playback.ClientCapabilities `json:"capabilities"`
	Quality       playback.QualityRequest     `json:"quality"`
	PositionMS    int64                       `json:"position_ms"`
	Observation   int64                       `json:"observation"`
	SmoothHandoff bool                        `json:"smooth_handoff,omitempty"`
}

type playbackHandoffRequest struct {
	Attached *bool `json:"attached"`
}

type playbackSubtitleRequest struct {
	Mode                string `json:"mode"`
	SubtitleStreamIndex *int   `json:"subtitle_stream_index"`
	SubtitleExternal    bool   `json:"subtitle_external,omitempty"`
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

func mediaProperties(item catalog.Item, selected catalog.AudioTrack, hasAudio bool) playback.MediaProperties {
	subtitles := make([]string, 0, len(item.Subtitles))
	for _, track := range item.Subtitles {
		subtitles = append(subtitles, track.Codec)
	}
	properties := playback.MediaProperties{Container: item.Container, VideoCodec: item.VideoCodec, VideoProfile: item.VideoProfile, VideoLevel: item.VideoLevel, Width: item.Width, Height: item.Height, VideoBitrate: item.Bitrate, FrameRateMilli: item.FrameRateMilli, BitDepth: item.BitDepth, HDR: item.HDR, AudioStreamIndex: -1, AudioSourceStreamIndex: -1, Subtitles: subtitles}
	if hasAudio {
		properties.AudioCodec, properties.AudioProfile = selected.Codec, selected.Profile
		properties.AudioChannels, properties.AudioSampleRate, properties.AudioBitrate = selected.Channels, selected.SampleRate, selected.Bitrate
		properties.AudioStreamIndex = selected.Index
		properties.AudioSourceStreamIndex = selected.SourceStreamIndex()
		properties.AudioExternal = selected.External
		properties.AudioSelected = true
		properties.RequiresAudioMapping = !sourceDefaultAudio(item.Audio, selected)
	}
	return properties
}

func selectedAudio(tracks []catalog.AudioTrack, requested *int, external bool, preferredLanguage string) (catalog.AudioTrack, bool) {
	if requested != nil {
		for _, track := range tracks {
			if track.Index == *requested && track.External == external && track.Index >= 0 {
				return track, true
			}
		}
		return catalog.AudioTrack{}, false
	}
	if external || len(tracks) == 0 {
		return catalog.AudioTrack{}, false
	}
	var preferred *catalog.AudioTrack
	bestScore := -1
	for index := range tracks {
		track := tracks[index]
		if preferredLanguage == "" || !strings.EqualFold(track.Language, preferredLanguage) {
			continue
		}
		score := 0
		if !strings.Contains(strings.ToLower(track.Title), "commentary") {
			score += 2
		}
		if track.Default {
			score++
		}
		if score > bestScore {
			candidate := track
			preferred = &candidate
			bestScore = score
		}
	}
	if preferred != nil {
		return *preferred, true
	}
	for _, track := range tracks {
		if !track.External && track.Default {
			return track, true
		}
	}
	return tracks[0], true
}

func selectedSubtitle(tracks []catalog.SubtitleTrack, requested *int, external bool, preference household.SubtitlePreference) (catalog.SubtitleTrack, bool) {
	if requested != nil {
		for _, track := range tracks {
			if track.Index == *requested && track.External == external && track.Index >= 0 && supportedSubtitleCodec(track.Codec) {
				return track, true
			}
		}
		return catalog.SubtitleTrack{}, false
	}
	if external || preference.Mode == household.SubtitleOff || len(tracks) == 0 {
		return catalog.SubtitleTrack{}, false
	}
	bestIndex, bestScore := -1, -1
	for index, track := range tracks {
		if !supportedSubtitleCodec(track.Codec) {
			continue
		}
		score := 0
		if preference.Language != "" && strings.EqualFold(track.Language, preference.Language) {
			score += 10
		}
		if track.Forced {
			score += 4
		}
		if preference.PreferSDH && track.SDH {
			score += 3
		}
		if track.Default {
			score += 2
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	if bestIndex < 0 {
		return catalog.SubtitleTrack{}, false
	}
	return tracks[bestIndex], true
}

func supportedSubtitleCodec(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "subrip", "srt", "webvtt", "ass", "ssa", "mov_text", "text":
		return true
	}
	return false
}

func playableSubtitleTracks(tracks []catalog.SubtitleTrack) []catalog.SubtitleTrack {
	out := make([]catalog.SubtitleTrack, 0, len(tracks))
	for _, track := range tracks {
		if track.Index >= 0 && supportedSubtitleCodec(track.Codec) {
			out = append(out, track)
		}
	}
	return out
}

func knownAudioLanguage(language string) bool {
	language = strings.TrimSpace(language)
	return language != "" && !strings.EqualFold(language, "und") && !strings.EqualFold(language, "unknown")
}

func sourceDefaultAudio(tracks []catalog.AudioTrack, selected catalog.AudioTrack) bool {
	if selected.External {
		return false
	}
	for _, track := range tracks {
		if !track.External && track.Default {
			return track.Index == selected.Index
		}
	}
	for _, track := range tracks {
		if !track.External {
			return track.Index == selected.Index
		}
	}
	return false
}

func subtitleSources(tracks []catalog.SubtitleTrack) []playback.SubtitleSource {
	sources := make([]playback.SubtitleSource, 0, len(tracks))
	for _, track := range tracks {
		if track.Index < 0 || !supportedSubtitleCodec(track.Codec) {
			continue
		}
		sources = append(sources, playback.SubtitleSource{Index: track.Index, SourceIndex: track.SourceStreamIndex(), SourceKey: track.SourceKey(), Codec: track.Codec, External: track.External})
	}
	return sources
}

func (s *Server) playbackPlan(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.profile(w, r) {
		return
	}
	var body playbackPlanRequest
	if !decode(r, &body) || body.CatalogID == "" || (body.ContinueWatchingIntent != "" && body.ContinueWatchingIntent != "user" && body.ContinueWatchingIntent != "recovery" && body.ContinueWatchingIntent != "automatic") {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !s.playableItem(w, r, body.CatalogID) {
		return
	}
	logical, err := s.playbackCatalogItem(r.Context(), r, body.CatalogID)
	if errors.Is(err, catalog.ErrCatalogNotFound) || (err == nil && (logical.Kind != "film" && logical.Kind != "episode")) {
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
	profile, _ := s.house.Profile(s.session(r))
	preferredLanguage, err := s.house.AudioLanguage(profile.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "playback_failed")
		return
	}
	requestedVersionID := body.VersionID
	if requestedVersionID == "" {
		requestedVersionID, err = s.catalog.PlaybackVersionPreference(profile.ID, body.CatalogID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "playback_failed")
			return
		}
	}
	chosen, versionCode, alternatives, err := s.choosePlaybackVersion(r, body, profile.ID, preferredLanguage, readiness)
	if err != nil {
		if versionCode != "" {
			status := http.StatusConflict
			if versionCode == "playback_version_incompatible" {
				status = http.StatusUnprocessableEntity
			}
			writePlaybackVersionError(w, status, versionCode, requestedVersionID, alternatives)
			return
		}
		if body.AudioStreamIndex != nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		playbackFailure(w, err)
		return
	}
	item, track, plan := chosen.item, chosen.track, chosen.plan
	preference, err := s.house.SubtitlePreference(profile.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "playback_failed")
		return
	}
	subtitle, hasSubtitle := selectedSubtitle(item.Subtitles, body.SubtitleStreamIndex, body.SubtitleExternal, preference)
	if body.SubtitleStreamIndex != nil && !hasSubtitle {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	plan.SubtitleSources = subtitleSources(item.Subtitles)
	plan.SubtitleSelected = hasSubtitle
	if hasSubtitle {
		plan.SubtitleSelectionIndex, plan.SubtitleExternal = subtitle.Index, subtitle.External
	}
	viewerID, ok := s.house.SessionIdentity(s.session(r))
	if !ok {
		fail(w, http.StatusForbidden, "profile_required")
		return
	}
	position, generation, err := s.house.ProgressState(s.session(r), body.CatalogID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
	}
	session, err := s.playback.CreateForViewer(viewerID, profile.ID, body.CatalogID, plan, position, generation+1)
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
	if _, _, err := s.house.BeginPlayback(profile.ID, body.CatalogID, generation); err != nil {
		s.playback.Stop(session.ID, profile.ID)
		if errors.Is(err, household.ErrProgressConflict) {
			fail(w, http.StatusConflict, "progress_conflict")
		} else {
			fail(w, http.StatusInternalServerError, "progress_failed")
		}
		return
	}
	if body.AudioStreamIndex != nil && knownAudioLanguage(track.Language) {
		if err := s.house.SaveAudioLanguage(profile.ID, track.Language); err != nil {
			s.playback.Stop(session.ID, profile.ID)
			fail(w, http.StatusInternalServerError, "playback_failed")
			return
		}
	}
	if r.Context().Err() != nil {
		s.playback.Stop(session.ID, profile.ID)
		return
	}
	if err := s.catalog.AcceptContinueWatching(profile.ID, body.CatalogID, body.ContinueWatchingIntent == "user"); err != nil {
		s.playback.Stop(session.ID, profile.ID)
		fail(w, http.StatusInternalServerError, "playback_failed")
		return
	}
	if body.VersionID != "" {
		if err := s.catalog.SavePlaybackVersionContext(r.Context(), profile.ID, body.CatalogID, chosen.version.ID); err != nil {
			s.playback.Stop(session.ID, profile.ID)
			fail(w, http.StatusInternalServerError, "playback_failed")
			return
		}
	}
	s.playback.StopSupersededPlans(session)
	write(w, http.StatusCreated, playbackResponse(session, item.Audio, item.Subtitles))
}

func completionEvent(catalogID string, item catalog.Item, completed bool) *household.ViewingEvent {
	if !completed || item.ID == "" {
		return nil
	}
	return &household.ViewingEvent{CatalogID: catalogID, Title: item.Title, Kind: item.Kind, Type: household.EventCompleted, Provenance: household.ProvenanceLocal}
}

func playbackResponse(session playback.Session, tracks []catalog.AudioTrack, subtitles []catalog.SubtitleTrack) map[string]any {
	subtitles = playableSubtitleTracks(subtitles)
	base := "/api/v1/playback/sessions/" + session.ID
	mediaURL := base + "/media"
	if session.Plan.Kind == playback.Remux || session.Plan.Kind == playback.Transcode {
		mediaURL = base + "/manifest.m3u8"
	}
	response := map[string]any{
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
		"audio_tracks":        tracks,
		"subtitle_tracks":     subtitles,
		"version":             session.Plan.Version,
	}
	if session.Plan.SubtitleSelected {
		for index := range subtitles {
			track := &subtitles[index]
			if track.Index == session.Plan.SubtitleSelectionIndex && track.External == session.Plan.SubtitleExternal {
				response["selected_subtitle"] = track
				response["subtitle_url"] = base + "/subtitle.vtt?index=" + strconv.Itoa(track.Index) + "&external=" + strconv.FormatBool(track.External)
				break
			}
		}
	}
	return response
}

func playbackFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playback.ErrUnsupported):
		fail(w, http.StatusUnprocessableEntity, "playback_unsupported")
	case errors.Is(err, playback.ErrInvalidQuality):
		fail(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, playback.ErrFFmpegUnavailable):
		fail(w, http.StatusServiceUnavailable, "ffmpeg_unavailable")
	case errors.Is(err, playback.ErrPreparing):
		fail(w, http.StatusServiceUnavailable, "playback_preparing")
	case errors.Is(err, playback.ErrCapacity):
		fail(w, http.StatusServiceUnavailable, "playback_capacity")
	case errors.Is(err, playback.ErrSessionInvalid):
		fail(w, http.StatusForbidden, "playback_session_invalid")
	case errors.Is(err, playback.ErrInvalidCapabilities):
		fail(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, playback.ErrUnknownCapability):
		fail(w, http.StatusUnprocessableEntity, "playback_capability_unknown")
	default:
		fail(w, http.StatusInternalServerError, "playback_failed")
	}
}

func (s *Server) playbackSession(w http.ResponseWriter, r *http.Request, touch bool) (playback.Session, bool) {
	if !s.profile(w, r) {
		return playback.Session{}, false
	}
	profile, _ := s.house.Profile(s.session(r))
	viewerID, valid := s.house.SessionIdentity(s.session(r))
	if !valid {
		fail(w, http.StatusForbidden, "profile_required")
		return playback.Session{}, false
	}
	session, ok := s.playback.LookupForViewer(r.PathValue("id"), viewerID, profile.ID, touch)
	if !ok {
		fail(w, http.StatusForbidden, "playback_session_invalid")
		return playback.Session{}, false
	}
	if !s.playableItem(w, r, session.CatalogID) {
		return playback.Session{}, false
	}
	return session, true
}

func (s *Server) rejectHandoffCandidate(w http.ResponseWriter, session playback.Session) bool {
	if !s.playback.IsHandoffCandidate(session.ID, session.ViewerID, session.ProfileID) {
		return false
	}
	playbackFailure(w, playback.ErrPreparing)
	return true
}

func (s *Server) rejectHandoffControl(w http.ResponseWriter, session playback.Session) bool {
	if !s.playback.IsHandoffControlBlocked(session.ID, session.ViewerID, session.ProfileID) {
		return false
	}
	playbackFailure(w, playback.ErrPreparing)
	return true
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

func (s *Server) playbackNext(w http.ResponseWriter, r *http.Request) {
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	includeSpecials := false
	switch r.URL.Query().Get("include_specials") {
	case "", "false":
	case "true":
		includeSpecials = true
	default:
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	next, err := s.catalog.EpisodeAfterAllowed(session.ProfileID, session.CatalogID, includeSpecials, func(item catalog.Item) (bool, error) {
		return s.itemAllowed(r, item.ID)
	})
	if errors.Is(err, catalog.ErrCatalogNotFound) {
		write(w, http.StatusOK, catalog.EpisodeSequence{State: catalog.EpisodeSequenceContextUnavailable})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "catalog_query_failed")
		return
	}
	if next.State == catalog.EpisodeSequenceNext && next.Episode != nil && session.Plan.VersionExplicit {
		policy, policyOK := s.requestPolicy(r)
		if !policyOK {
			fail(w, http.StatusForbidden, "profile_required")
			return
		}
		versions, versionErr := s.catalog.MediaVersions(r.Context(), session.ProfileID, next.Episode.ID, policy)
		if versionErr != nil {
			fail(w, http.StatusInternalServerError, "catalog_query_failed")
			return
		}
		found := false
		alternatives := []catalog.MediaVersion{}
		for _, version := range versions {
			if version.ID == session.Plan.VersionID && version.Available {
				found = true
				continue
			}
			if version.Available {
				version.Selected = false
				alternatives = append(alternatives, version)
			}
		}
		if found {
			next.SelectedVersionID = session.Plan.VersionID
		} else {
			next.State = catalog.EpisodeSequenceVersionUnavailable
			next.RequestedVersionID = session.Plan.VersionID
			next.Alternatives = alternatives
		}
	}
	write(w, http.StatusOK, next)
}

func (s *Server) playbackManifest(w http.ResponseWriter, r *http.Request) {
	s.servePlaybackAsset(w, r, "master.m3u8")
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
	file, err := s.playback.OpenAssetForViewer(session.ID, session.ViewerID, session.ProfileID, name)
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
		manifest, readErr := io.ReadAll(file)
		if readErr != nil {
			fail(w, http.StatusNotFound, "playback_asset_not_found")
			return
		}
		// Native HLS otherwise defaults to the sliding playlist's live edge,
		// downloading later fragments before seeking back to the session start.
		manifest = bytes.Replace(manifest, []byte("#EXTM3U\n"), []byte("#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n"), 1)
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(manifest)
		return
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
	if s.rejectHandoffCandidate(w, session) {
		return
	}
	var body playbackPositionRequest
	if !decode(r, &body) || body.PositionMS < 0 || body.Observation < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, itemErr := s.sessionPlaybackCatalogItem(r.Context(), r, session)
	completed := body.Ended || (itemErr == nil && item.DurationMS > 0 && body.PositionMS >= item.DurationMS-item.DurationMS/10)
	unlock := s.lockPlaybackProgress(session.ProfileID, session.CatalogID)
	defer unlock()
	accepted, err := s.house.RecordPlaybackProgress(session.ProfileID, session.CatalogID, body.PositionMS, session.ProgressGeneration, body.Observation, completed, completionEvent(session.CatalogID, item, completed))
	if err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
	}
	updated := session
	if accepted {
		updated, err = s.playback.HeartbeatForViewer(session.ID, session.ViewerID, session.ProfileID, body.PositionMS)
		if err != nil {
			playbackFailure(w, err)
			return
		}
	}

	write(w, http.StatusOK, map[string]any{"accepted": accepted, "expires_at": updated.ExpiresAt.Unix()})
}

func (s *Server) playbackSeek(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	if s.rejectHandoffControl(w, session) {
		return
	}
	var body playbackPositionRequest
	if !decode(r, &body) || body.PositionMS < 0 || body.Observation < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, itemErr := s.sessionPlaybackCatalogItem(r.Context(), r, session)
	completed := body.Ended || (itemErr == nil && item.DurationMS > 0 && body.PositionMS >= item.DurationMS-item.DurationMS/10)
	unlock := s.lockPlaybackProgress(session.ProfileID, session.CatalogID)
	defer unlock()
	accepted, err := s.house.CanRecordPlaybackProgress(session.ProfileID, session.CatalogID, session.ProgressGeneration, body.Observation)
	if err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
	}
	updated := session
	if accepted {
		var progressErr error
		updated, err = s.playback.SeekForViewerContextWithCommit(r.Context(), session.ID, session.ViewerID, session.ProfileID, body.PositionMS, func() error {
			accepted, progressErr = s.house.RecordPlaybackProgress(session.ProfileID, session.CatalogID, body.PositionMS, session.ProgressGeneration, body.Observation, completed, completionEvent(session.CatalogID, item, completed))
			if progressErr != nil {
				return progressErr
			}
			if !accepted {
				return household.ErrProgressConflict
			}
			return nil
		})
		if progressErr != nil {
			fail(w, http.StatusInternalServerError, "progress_failed")
			return
		}
		if errors.Is(err, household.ErrProgressConflict) {
			accepted = false
			updated = session
		} else if err != nil {
			playbackFailure(w, err)
			return
		}
	}

	write(w, http.StatusOK, playbackResponse(updated, item.Audio, item.Subtitles))
}

func (s *Server) playbackAudio(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	if s.rejectHandoffControl(w, session) {
		return
	}
	var body playbackAudioRequest
	if !decode(r, &body) || body.AudioStreamIndex == nil || body.PositionMS < 0 || body.Observation < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, err := s.sessionPlaybackCatalogItem(r.Context(), r, session)
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	track, found := selectedAudio(item.Audio, body.AudioStreamIndex, body.AudioExternal, "")
	if !found {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.readyMu.RLock()
	readiness := playback.ServerReadiness{FFmpeg: s.readiness.FFmpeg}
	s.readyMu.RUnlock()
	quality := playback.QualityRequest{Mode: session.Plan.QualityMode}
	if session.Plan.QualityMode != playback.QualityOriginal {
		quality.MaxVideoBitrate, quality.MaxWidth, quality.MaxHeight = session.Plan.QualityMaxVideoBitrate, session.Plan.QualityMaxWidth, session.Plan.QualityMaxHeight
	}
	plan, err := playback.PlanForQuality(mediaProperties(item, track, true), body.Capabilities, readiness, quality)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	plan.SourceKey = session.Plan.SourceKey
	plan.SubtitleSources = session.Plan.SubtitleSources
	plan.SubtitleSelectionIndex = session.Plan.SubtitleSelectionIndex
	plan.SubtitleExternal = session.Plan.SubtitleExternal
	plan.SubtitleSelected = session.Plan.SubtitleSelected
	profile, _ := s.house.Profile(s.session(r))
	progressFailed := false
	updated, err := s.playback.ReplaceContextWithCommit(r.Context(), session.ID, profile.ID, plan, body.PositionMS, func() error {
		accepted, progressErr := s.house.RecordPlaybackProgress(profile.ID, session.CatalogID, body.PositionMS, session.ProgressGeneration, body.Observation, false)
		if progressErr != nil {
			progressFailed = true
			return progressErr
		}
		if !accepted {
			return household.ErrProgressConflict
		}
		return nil
	})
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if progressFailed {
			fail(w, http.StatusInternalServerError, "progress_failed")
			return
		}
		if errors.Is(err, household.ErrProgressConflict) {
			fail(w, http.StatusConflict, "progress_conflict")
			return
		}
		playbackFailure(w, err)
		return
	}
	if knownAudioLanguage(track.Language) {
		if err := s.house.SaveAudioLanguage(profile.ID, track.Language); err != nil {
			s.playback.Stop(updated.ID, profile.ID)
			fail(w, http.StatusInternalServerError, "playback_failed")
			return
		}
	}
	write(w, http.StatusOK, playbackResponse(updated, item.Audio, item.Subtitles))
}

func (s *Server) playbackQuality(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	if s.rejectHandoffControl(w, session) {
		return
	}
	var body playbackQualityRequest
	if !decode(r, &body) || body.PositionMS < 0 || body.Observation < 0 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, err := s.sessionPlaybackCatalogItem(r.Context(), r, session)
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	var track catalog.AudioTrack
	hasAudio := session.Plan.AudioSelected
	if hasAudio {
		index := session.Plan.AudioStreamIndex
		track, hasAudio = selectedAudio(item.Audio, &index, session.Plan.AudioExternal, "")
		if !hasAudio {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	s.readyMu.RLock()
	readiness := playback.ServerReadiness{FFmpeg: s.readiness.FFmpeg}
	s.readyMu.RUnlock()
	plan, err := playback.PlanForQuality(mediaProperties(item, track, hasAudio), body.Capabilities, readiness, body.Quality)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	plan.SourceKey = session.Plan.SourceKey
	plan.SubtitleSources = session.Plan.SubtitleSources
	plan.SubtitleSelectionIndex = session.Plan.SubtitleSelectionIndex
	plan.SubtitleExternal = session.Plan.SubtitleExternal
	plan.SubtitleSelected = session.Plan.SubtitleSelected
	profile, _ := s.house.Profile(s.session(r))
	progressFailed := false
	commitProgress := func() error {
		accepted, progressErr := s.house.RecordPlaybackProgress(profile.ID, session.CatalogID, body.PositionMS, session.ProgressGeneration, body.Observation, false)
		if progressErr != nil {
			progressFailed = true
			return progressErr
		}
		if !accepted {
			return household.ErrProgressConflict
		}
		return nil
	}
	updated := playback.Session{}
	handoffID := ""
	if body.SmoothHandoff {
		handoff, handoffErr := s.playback.PrepareHandoffContextWithCommit(r.Context(), session.ID, profile.ID, plan, body.PositionMS, commitProgress)
		err = handoffErr
		updated, handoffID = handoff.Session, handoff.ID
	} else {
		updated, err = s.playback.ReplaceContextWithCommit(r.Context(), session.ID, profile.ID, plan, body.PositionMS, commitProgress)
	}
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if progressFailed {
			fail(w, http.StatusInternalServerError, "progress_failed")
			return
		}
		if errors.Is(err, household.ErrProgressConflict) {
			fail(w, http.StatusConflict, "progress_conflict")
			return
		}
		playbackFailure(w, err)
		return
	}
	if r.Context().Err() != nil {
		if handoffID != "" {
			_ = s.playback.ResolveHandoff(handoffID, session.ViewerID, profile.ID, false)
		}
		return
	}
	response := playbackResponse(updated, item.Audio, item.Subtitles)
	if handoffID != "" {
		response["handoff_url"] = "/api/v1/playback/handoffs/" + handoffID
	}
	write(w, http.StatusOK, response)
}

func (s *Server) playbackHandoff(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.profile(w, r) {
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	viewerID, ok := s.house.SessionIdentity(s.session(r))
	if !ok {
		fail(w, http.StatusForbidden, "profile_required")
		return
	}
	var body playbackHandoffRequest
	if !decode(r, &body) || body.Attached == nil {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	err := s.playback.ResolveHandoff(r.PathValue("id"), viewerID, profile.ID, *body.Attached)
	if errors.Is(err, playback.ErrHandoffConflict) {
		fail(w, http.StatusConflict, "playback_handoff_conflict")
		return
	}
	if err != nil {
		playbackFailure(w, err)
		return
	}
	write(w, http.StatusOK, map[string]bool{"attached": *body.Attached})
}

func (s *Server) playbackSubtitleSelection(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	if s.rejectHandoffControl(w, session) {
		return
	}
	var body playbackSubtitleRequest
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	item, err := s.sessionPlaybackCatalogItem(r.Context(), r, session)
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	preference, err := s.house.SubtitlePreference(session.ProfileID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "playback_failed")
		return
	}
	var track catalog.SubtitleTrack
	selected := false
	switch body.Mode {
	case "off":
		preference.Mode = household.SubtitleOff
	case "automatic":
		preference.Mode = household.SubtitleAutomatic
		track, selected = selectedSubtitle(item.Subtitles, nil, false, preference)
	case "track":
		if body.SubtitleStreamIndex == nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		track, selected = selectedSubtitle(item.Subtitles, body.SubtitleStreamIndex, body.SubtitleExternal, preference)
		if !selected {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		preference.Mode, preference.Language, preference.PreferSDH = household.SubtitleAutomatic, strings.ToLower(strings.TrimSpace(track.Language)), track.SDH
	default:
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	updated, err := s.playback.SelectSubtitleWithCommit(session.ID, session.ViewerID, session.ProfileID, track.Index, track.External, selected, func() error {
		return s.house.SaveSubtitlePreference(session.ProfileID, preference)
	})
	if err != nil {
		playbackFailure(w, err)
		return
	}
	write(w, http.StatusOK, playbackResponse(updated, item.Audio, item.Subtitles))
}

func (s *Server) playbackSubtitle(w http.ResponseWriter, r *http.Request) {
	session, ok := s.playbackSession(w, r, true)
	if !ok {
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("index"))
	external, boolErr := strconv.ParseBool(r.URL.Query().Get("external"))
	if err != nil || boolErr != nil {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var source *playback.SubtitleSource
	for candidateIndex := range session.Plan.SubtitleSources {
		candidate := &session.Plan.SubtitleSources[candidateIndex]
		if candidate.Index == index && candidate.External == external {
			source = candidate
			break
		}
	}
	if source == nil {
		fail(w, http.StatusNotFound, "playback_asset_not_found")
		return
	}
	captionContext, release, err := s.playback.SubtitleContext(r.Context(), session.ID, session.ViewerID, session.ProfileID)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	defer release()
	file, err := s.catalog.OpenSubtitleSource(session.CatalogID, session.Plan.SourceKey, source.SourceKey, source.Index, source.External)
	if err != nil {
		fail(w, http.StatusNotFound, "playback_asset_not_found")
		return
	}
	defer file.Close()
	var input []byte
	codec := source.Codec
	if source.External {
		input, err = io.ReadAll(io.LimitReader(file, captions.MaxInputBytes+1))
	} else {
		input, err = captions.Extract(captionContext, file, source.SourceIndex)
		codec = "webvtt"
	}
	if err != nil {
		if captionContext.Err() != nil {
			return
		}
		fail(w, http.StatusInternalServerError, "playback_failed")
		return
	}
	if captionContext.Err() != nil {
		return
	}
	output, err := captions.Convert(input, codec, session.StreamOffsetMS)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "playback_unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

func (s *Server) playbackStop(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	if !s.playback.StopForViewer(session.ID, session.ViewerID, session.ProfileID) {
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
	catalogID, sourceKey, audioStreamIndex, external, ok := s.playback.InputFile(r.PathValue("token"))
	if !ok {
		fail(w, http.StatusForbidden, "playback_input_invalid")
		return
	}
	file, err := s.catalog.OpenAudioSource(catalogID, sourceKey, audioStreamIndex, external)
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
