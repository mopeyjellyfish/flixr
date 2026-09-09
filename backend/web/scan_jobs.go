package web

import (
	"errors"
	"net/http"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func (s *Server) libraryScanPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	libraryID := r.PathValue("id")
	if r.Method == http.MethodGet {
		policy, err := s.catalog.ScanPolicy(libraryID)
		if err != nil {
			scanPolicyFailure(w, err)
			return
		}
		write(w, http.StatusOK, map[string]any{"policy": policy})
		return
	}
	var policy catalog.ScanPolicy
	if !decode(r, &policy) {
		fail(w, http.StatusBadRequest, "invalid_scan_policy")
		return
	}
	var saved catalog.ScanPolicy
	var err error
	if s.settingsLocks["background.scan_schedule"] {
		saved, err = s.catalog.SetScanExclusions(libraryID, policy.Exclusions)
	} else {
		saved, err = s.catalog.SetScanPolicy(libraryID, policy)
	}
	if err != nil {
		scanPolicyFailure(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"policy": saved})
}

func scanPolicyFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrLibraryNotFound) {
		fail(w, http.StatusNotFound, "library_not_found")
	} else {
		fail(w, http.StatusBadRequest, "invalid_scan_policy")
	}
}

func (s *Server) scanJobs(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		jobs, err := s.catalog.ScanJobs(50)
		if err != nil {
			fail(w, http.StatusInternalServerError, "scan_jobs_failed")
			return
		}
		write(w, http.StatusOK, map[string]any{"jobs": jobs})
		return
	}
	if !s.ffprobeReady() {
		fail(w, http.StatusServiceUnavailable, "ffprobe_unavailable")
		return
	}
	var request struct {
		LibraryID string `json:"library_id"`
	}
	if !decode(r, &request) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if request.LibraryID == "" {
		jobs, err := s.catalog.QueueAllLibraryScans("manual")
		if err != nil {
			fail(w, http.StatusInternalServerError, "scan_start_failed")
			return
		}
		write(w, http.StatusAccepted, map[string]any{"jobs": jobs})
		return
	}
	job, err := s.catalog.QueueLibraryScan(request.LibraryID, "manual")
	if errors.Is(err, catalog.ErrLibraryNotFound) {
		fail(w, http.StatusNotFound, "library_not_found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "scan_start_failed")
		return
	}
	write(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) scanJob(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodDelete {
		if err := s.catalog.CancelScanJob(id); err != nil {
			fail(w, http.StatusConflict, "scan_job_not_active")
			return
		}
		job, _ := s.catalog.ScanJob(id)
		write(w, http.StatusOK, map[string]any{"job": job})
		return
	}
	job, err := s.catalog.ScanJob(id)
	if err != nil {
		fail(w, http.StatusNotFound, "scan_job_not_found")
		return
	}
	write(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) retryScanJob(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var request struct {
		Files []catalog.ScanJobFile `json:"files"`
	}
	if !decode(r, &request) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	job, err := s.catalog.RetryScanJob(r.PathValue("id"), request.Files)
	if err != nil {
		fail(w, http.StatusConflict, "scan_job_not_retryable")
		return
	}
	write(w, http.StatusAccepted, map[string]any{"job": job})
}
