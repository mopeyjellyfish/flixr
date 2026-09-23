package web

import (
	"errors"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"net/http"
	"strconv"
)

func (s *Server) viewer(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	policy, ok := s.requestPolicy(r)
	if !ok {
		fail(w, http.StatusForbidden, "profile_required")
		return
	}
	stateKind, stateID := r.URL.Query().Get("state_kind"), r.URL.Query().Get("state_id")
	if stateKind != "" || stateID != "" {
		if stateKind == "" || stateID == "" {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if s.catalog.IsMediaVersionMember(stateKind, stateID) {
			fail(w, http.StatusNotFound, "catalog_not_found")
			return
		}
		state, err := s.catalog.ViewerItemState(r.Context(), profile.ID, stateKind, stateID, policy)
		if errors.Is(err, catalog.ErrCatalogNotFound) || errors.Is(err, catalog.ErrAccessDenied) {
			fail(w, http.StatusNotFound, "catalog_not_found")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "catalog_query_failed")
			return
		}
		write(w, http.StatusOK, map[string]any{"state": state})
		return
	}
	limit := 48
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		limit = parsed
	}
	model, err := s.catalog.ViewerPage(r.Context(), profile.ID, r.URL.Query().Get("media"), r.URL.Query().Get("section"), r.URL.Query().Get("cursor"), limit, policy)
	if errors.Is(err, catalog.ErrInvalidViewerMode) || errors.Is(err, catalog.ErrInvalidViewerPage) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "catalog_query_failed")
		return
	}
	write(w, http.StatusOK, model)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if kind != "film" && kind != "series" {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	if !s.publicContent(w, r, kind, id) {
		return
	}
	if err := s.catalog.SetListed(profile.ID, kind, id, r.Method == http.MethodPut); err != nil {
		if errors.Is(err, catalog.ErrCatalogNotFound) {
			fail(w, http.StatusNotFound, "catalog_not_found")
		} else {
			fail(w, http.StatusInternalServerError, "catalog_list_failed")
		}
		return
	}
	write(w, http.StatusOK, map[string]bool{"listed": r.Method == http.MethodPut})
}

func (s *Server) continueWatching(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if kind != "film" && kind != "series" {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	if !s.publicContent(w, r, kind, id) {
		return
	}
	dismissed := r.Method == http.MethodDelete
	if err := s.catalog.SetContinueWatchingDismissed(profile.ID, kind, id, dismissed); err != nil {
		if errors.Is(err, catalog.ErrCatalogNotFound) {
			fail(w, http.StatusNotFound, "catalog_not_found")
		} else {
			fail(w, http.StatusInternalServerError, "catalog_continue_watching_failed")
		}
		return
	}
	write(w, http.StatusOK, map[string]bool{"dismissed": dismissed})
}

func (s *Server) watched(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	var body struct {
		Watched bool `json:"watched"`
		Season  int  `json:"season"`
	}
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	ids, err := s.catalog.WatchedItems(r.PathValue("kind"), r.PathValue("id"), body.Season)
	if errors.Is(err, catalog.ErrCatalogNotFound) {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "catalog_query_failed")
		return
	}
	allowedIDs := ids[:0]
	for _, id := range ids {
		allowed, accessErr := s.itemAllowed(r, id)
		if accessErr != nil {
			fail(w, http.StatusInternalServerError, "catalog_query_failed")
			return
		}
		if allowed {
			allowedIDs = append(allowedIDs, id)
		}
	}
	ids = allowedIDs
	if len(ids) == 0 {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	if err := s.house.SetWatched(profile.ID, ids, body.Watched); err != nil {
		fail(w, http.StatusInternalServerError, "progress_failed")
		return
	}
	write(w, http.StatusOK, map[string]any{"watched": body.Watched, "items": len(ids)})
}

func (s *Server) preferences(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	media := r.PathValue("media")
	if r.Method == http.MethodGet {
		preference, err := s.catalog.Preference(profile.ID, media)
		if errors.Is(err, catalog.ErrInvalidViewerMode) {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "catalog_preferences_failed")
			return
		}
		write(w, http.StatusOK, preference)
		return
	}
	var preference catalog.ViewPreference
	if !decode(r, &preference) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	preference, err := s.catalog.SavePreference(profile.ID, media, preference)
	if errors.Is(err, catalog.ErrInvalidViewerMode) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "catalog_preferences_failed")
		return
	}
	write(w, http.StatusOK, preference)
}
