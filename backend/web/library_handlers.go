package web

import (
	"errors"
	"net/http"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func (s *Server) libraries(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		s.writeLibraries(w)
		return
	}
	var request struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if !decode(r, &request) {
		fail(w, http.StatusBadRequest, "invalid_library")
		return
	}
	library, err := s.catalog.CreateLibrary(request.Name, request.Kind)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_library")
		return
	}
	write(w, http.StatusCreated, library)
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodPatch {
		var request struct {
			Name string `json:"name"`
		}
		if !decode(r, &request) {
			fail(w, http.StatusBadRequest, "invalid_library")
			return
		}
		library, err := s.catalog.RenameLibrary(r.PathValue("id"), request.Name)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_library")
			return
		}
		write(w, http.StatusOK, library)
		return
	}
	if err := s.catalog.DeleteLibrary(r.PathValue("id")); err != nil {
		if errors.Is(err, catalog.ErrLibraryHasLocations) {
			fail(w, http.StatusConflict, "library_has_locations")
		} else {
			fail(w, http.StatusNotFound, "library_not_found")
		}
		return
	}
	write(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) libraryLocations(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if !decode(r, &request) {
		fail(w, http.StatusBadRequest, "invalid_library_location")
		return
	}
	location, err := s.catalog.AddLibraryLocation(r.PathValue("id"), request.Path)
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrScanActive):
			fail(w, http.StatusConflict, "scan_active")
		case errors.Is(err, catalog.ErrOverlappingLocation):
			fail(w, http.StatusConflict, "library_location_overlap")
		default:
			fail(w, http.StatusBadRequest, "invalid_library_location")
		}
		return
	}
	write(w, http.StatusCreated, location)
}

func (s *Server) previewLibraryLocationChange(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if !decode(r, &request) {
		fail(w, http.StatusBadRequest, "invalid_library_location")
		return
	}
	preview, err := s.catalog.PreviewLocationChange(r.PathValue("id"), request.Path)
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrScanActive):
			fail(w, http.StatusConflict, "scan_active")
		case errors.Is(err, catalog.ErrOverlappingLocation):
			fail(w, http.StatusConflict, "library_location_overlap")
		default:
			fail(w, http.StatusBadRequest, "invalid_library_location")
		}
		return
	}
	write(w, http.StatusOK, preview)
}

func (s *Server) confirmLibraryLocationChange(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if err := s.catalog.ConfirmLocationChange(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, catalog.ErrRemovalReviewNotFound) {
			fail(w, http.StatusConflict, "library_change_changed")
		} else if errors.Is(err, catalog.ErrOverlappingLocation) {
			fail(w, http.StatusConflict, "library_location_overlap")
		} else if errors.Is(err, catalog.ErrScanActive) {
			fail(w, http.StatusConflict, "scan_active")
		} else {
			fail(w, http.StatusInternalServerError, "library_change_failed")
		}
		return
	}
	s.writeLibraries(w)
}

func (s *Server) writeLibraries(w http.ResponseWriter) {
	libraries, err := s.catalog.Libraries()
	if err != nil {
		fail(w, http.StatusInternalServerError, "libraries_failed")
		return
	}
	write(w, http.StatusOK, map[string]any{"libraries": libraries})
}
