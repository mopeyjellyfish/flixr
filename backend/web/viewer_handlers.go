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
