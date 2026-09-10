package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/playback"
)

type chosenPlaybackVersion struct {
	item     catalog.Item
	version  catalog.MediaVersion
	track    catalog.AudioTrack
	hasAudio bool
	plan     playback.Plan
}

func planRank(kind playback.Kind) int {
	switch kind {
	case playback.Direct:
		return 0
	case playback.Remux:
		return 1
	default:
		return 2
	}
}

func compatibleAlternatives(choices []catalog.PlaybackVersion, requested string, body playbackPlanRequest, preferredLanguage string, readiness playback.ServerReadiness) []catalog.MediaVersion {
	out, seen := []catalog.MediaVersion{}, map[string]bool{}
	for _, choice := range choices {
		if choice.Version.ID == requested || seen[choice.Version.ID] || !choice.Version.Available {
			continue
		}
		track, hasAudio := selectedAudio(choice.Item.Audio, body.AudioStreamIndex, body.AudioExternal, preferredLanguage)
		if body.AudioStreamIndex != nil && !hasAudio {
			continue
		}
		if _, err := playback.PlanForQuality(mediaProperties(choice.Item, track, hasAudio), body.Capabilities, readiness, body.Quality); err != nil {
			continue
		}
		choice.Version.Selected = false
		out = append(out, choice.Version)
		seen[choice.Version.ID] = true
	}
	return out
}

func (s *Server) choosePlaybackVersion(r *http.Request, body playbackPlanRequest, profileID, preferredLanguage string, readiness playback.ServerReadiness) (chosenPlaybackVersion, string, []catalog.MediaVersion, error) {
	policy, ok := s.requestPolicy(r)
	if !ok {
		return chosenPlaybackVersion{}, "", nil, catalog.ErrAccessDenied
	}
	choices, err := s.catalog.PlaybackVersions(r.Context(), profileID, body.CatalogID, policy)
	if err != nil {
		return chosenPlaybackVersion{}, "", nil, err
	}
	requested := body.VersionID
	if requested == "" {
		for _, choice := range choices {
			if choice.Version.Selected {
				requested = choice.Version.ID
				break
			}
		}
	}
	explicit := requested != ""
	found, available := false, false
	best, hasBest := chosenPlaybackVersion{}, false
	var planningErr error
	for _, choice := range choices {
		if explicit && choice.Version.ID != requested {
			continue
		}
		found = true
		if !choice.Version.Available {
			continue
		}
		available = true
		track, hasAudio := selectedAudio(choice.Item.Audio, body.AudioStreamIndex, body.AudioExternal, preferredLanguage)
		if body.AudioStreamIndex != nil && !hasAudio {
			continue
		}
		plan, planErr := playback.PlanForQuality(mediaProperties(choice.Item, track, hasAudio), body.Capabilities, readiness, body.Quality)
		if planErr != nil {
			planningErr = planErr
			continue
		}
		candidate := chosenPlaybackVersion{item: choice.Item, version: choice.Version, track: track, hasAudio: hasAudio, plan: plan}
		if !hasBest || planRank(plan.Kind) < planRank(best.plan.Kind) || (planRank(plan.Kind) == planRank(best.plan.Kind) && choice.Item.Bitrate > best.item.Bitrate) {
			best, hasBest = candidate, true
		}
	}
	alternatives := compatibleAlternatives(choices, requested, body, preferredLanguage, readiness)
	if !hasBest {
		if explicit && (!found || !available) {
			return chosenPlaybackVersion{}, "playback_version_unavailable", alternatives, catalog.ErrMediaVersionUnavailable
		}
		if explicit {
			return chosenPlaybackVersion{}, "playback_version_incompatible", alternatives, catalog.ErrMediaVersionConflict
		}
		if planningErr != nil {
			return chosenPlaybackVersion{}, "", alternatives, planningErr
		}
		return chosenPlaybackVersion{}, "", alternatives, errors.New("no compatible playback source")
	}
	best.plan.SourceKey = best.item.SourceKey()
	best.plan.VersionID = best.version.ID
	best.plan.VersionExplicit = explicit
	if explicit {
		best.version.Selected = true
	}
	best.plan.Version = playbackMediaVersion(best.version)
	return best, "", alternatives, nil
}

type mediaVersionGroupRequest struct {
	Kind        string   `json:"kind"`
	CanonicalID string   `json:"canonical_id"`
	MemberIDs   []string `json:"member_ids"`
}

type mediaVersionLabelRequest struct {
	EditionLabel string `json:"edition_label"`
}

func mediaVersionQuery(r *http.Request, key, fallback string) string {
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	return fallback
}

func (s *Server) mediaVersionGroups(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		offset, errOffset := strconv.Atoi(mediaVersionQuery(r, "offset", "0"))
		limit, errLimit := strconv.Atoi(mediaVersionQuery(r, "limit", "50"))
		if errOffset != nil || errLimit != nil || offset < 0 || limit < 1 || limit > 100 {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		groups, err := s.catalog.MediaVersionGroupsPage(r.URL.Query().Get("q"), offset, limit)
		if err != nil {
			fail(w, http.StatusInternalServerError, "media_version_groups_failed")
			return
		}
		write(w, http.StatusOK, groups)
		return
	}
	var body mediaVersionGroupRequest
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	group, err := s.catalog.CreateMediaVersionGroup(r.Context(), body.Kind, body.CanonicalID, body.MemberIDs)
	if errors.Is(err, catalog.ErrCatalogNotFound) {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	if errors.Is(err, catalog.ErrMediaVersionConflict) {
		fail(w, http.StatusConflict, "media_version_conflict")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "media_version_groups_failed")
		return
	}
	s.playback.StopCatalogIDs(s.catalog.MediaVersionPlaybackIDs(body.Kind, append([]string{body.CanonicalID}, body.MemberIDs...)...))
	write(w, http.StatusCreated, group)
}

func (s *Server) mediaVersionGroup(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var body mediaVersionLabelRequest
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	group, err := s.catalog.SetEditionLabel(r.Context(), r.PathValue("kind"), r.PathValue("id"), body.EditionLabel)
	if errors.Is(err, catalog.ErrCatalogNotFound) {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	if errors.Is(err, catalog.ErrMediaVersionConflict) {
		fail(w, http.StatusConflict, "media_version_conflict")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "media_version_groups_failed")
		return
	}
	s.playback.StopCatalogIDs(s.catalog.MediaVersionPlaybackIDs(r.PathValue("kind"), r.PathValue("id")))
	write(w, http.StatusOK, group)
}

func (s *Server) mediaVersionMember(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	group, err := s.catalog.UngroupMediaVersion(r.Context(), r.PathValue("kind"), r.PathValue("id"), r.PathValue("member"))
	if errors.Is(err, catalog.ErrCatalogNotFound) {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	if errors.Is(err, catalog.ErrMediaVersionConflict) {
		fail(w, http.StatusConflict, "media_version_conflict")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "media_version_groups_failed")
		return
	}
	s.playback.StopCatalogIDs(s.catalog.MediaVersionPlaybackIDs(r.PathValue("kind"), r.PathValue("id"), r.PathValue("member")))
	write(w, http.StatusOK, map[string]any{"group": group, "ungrouped_id": r.PathValue("member")})
}

func playbackMediaVersion(version catalog.MediaVersion) playback.MediaVersion {
	return playback.MediaVersion{ID: version.ID, Label: version.Label, EditionID: version.EditionID, EditionLabel: version.EditionLabel, Width: version.Width, Height: version.Height, HDR: version.HDR, VideoCodec: version.VideoCodec, Container: version.Container, Bitrate: version.Bitrate, Selected: version.Selected, Available: version.Available}
}

func (s *Server) detailVersions(r *http.Request, catalogID string) ([]catalog.MediaVersion, error) {
	profile, ok := s.house.Profile(s.session(r))
	if !ok {
		return nil, catalog.ErrAccessDenied
	}
	policy, ok := s.requestPolicy(r)
	if !ok {
		return nil, catalog.ErrAccessDenied
	}
	return s.catalog.MediaVersions(r.Context(), profile.ID, catalogID, policy)
}

type playbackVersionError struct {
	Code               string                 `json:"code"`
	RequestedVersionID string                 `json:"requested_version_id"`
	Alternatives       []catalog.MediaVersion `json:"alternatives"`
}

func writePlaybackVersionError(w http.ResponseWriter, status int, code, requested string, alternatives []catalog.MediaVersion) {
	if alternatives == nil {
		alternatives = []catalog.MediaVersion{}
	}
	write(w, status, map[string]any{"error": playbackVersionError{Code: code, RequestedVersionID: requested, Alternatives: alternatives}})
}
