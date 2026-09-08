package web

import (
	"errors"
	"net/http"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func (s *Server) viewer(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	model, err := s.catalog.Viewer(profile.ID, r.URL.Query().Get("media"))
	if errors.Is(err, catalog.ErrInvalidViewerMode) {
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
