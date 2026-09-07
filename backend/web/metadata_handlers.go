package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func (s *Server) unmatchedMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	write(w, http.StatusOK, map[string]any{"items": s.catalog.MetadataTargets()})
}

func (s *Server) metadataCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	candidates, err := s.catalog.Candidates(ctx, r.PathValue("kind"), r.PathValue("id"), r.URL.Query().Get("q"), r.URL.Query().Get("language"), r.URL.Query().Get("region"))
	if err != nil {
		s.metadataError(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"candidates": candidates})
}

func (s *Server) metadataMatch(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var body struct {
		ProviderID string `json:"provider_id"`
		Language   string `json:"language"`
		Region     string `json:"region"`
	}
	if !decode(r, &body) || strings.TrimSpace(body.ProviderID) == "" {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	item, err := s.catalog.Match(ctx, r.PathValue("kind"), r.PathValue("id"), body.ProviderID, body.Language, body.Region)
	if err != nil {
		s.metadataError(w, err)
		return
	}
	write(w, http.StatusOK, item)
}

func (s *Server) metadataUnmatch(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	item, err := s.catalog.Unmatch(r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		s.metadataError(w, err)
		return
	}
	write(w, http.StatusOK, item)
}

func (s *Server) metadataFields(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	fields, err := s.catalog.MetadataFields(r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		s.metadataError(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"fields": fields})
}
func (s *Server) metadataPreview(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var edit catalog.MetadataEdit
	if !decode(r, &edit) {
		fail(w, 400, "invalid_request")
		return
	}
	fields, err := s.catalog.PreviewMetadata(r.PathValue("kind"), r.PathValue("id"), edit)
	if err != nil {
		s.metadataError(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"fields": fields})
}
func (s *Server) metadataEdit(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var edit catalog.MetadataEdit
	if !decode(r, &edit) {
		fail(w, 400, "invalid_request")
		return
	}
	item, err := s.catalog.EditMetadata(r.PathValue("kind"), r.PathValue("id"), edit)
	if err != nil {
		s.metadataError(w, err)
		return
	}
	write(w, http.StatusOK, item)
}

func (s *Server) metadataError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrMetadataNotFound) {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return
	}
	if errors.Is(err, catalog.ErrMetadataBusy) {
		fail(w, http.StatusConflict, "metadata_busy")
		return
	}
	// Provider failures leave the local title untouched; the owner can retry from the queue.
	fail(w, http.StatusServiceUnavailable, "metadata_unavailable")
}
