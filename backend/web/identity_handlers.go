package web

import (
	"errors"
	"net/http"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func (s *Server) identityRepairs(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	repairs, err := s.catalog.IdentityRepairs()
	if err != nil {
		s.identityError(w, err)
		return
	}
	write(w, http.StatusOK, repairs)
}
func (s *Server) identityMerge(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var body struct {
		Kind       string `json:"kind"`
		SurvivorID string `json:"survivor_id"`
		SourceID   string `json:"source_id"`
	}
	if !decode(r, &body) || body.SurvivorID == "" || body.SourceID == "" || (body.Kind != "film" && body.Kind != "episode" && body.Kind != "series") {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	merge, err := s.catalog.MergeIdentity(body.Kind, body.SurvivorID, body.SourceID)
	if err != nil {
		s.identityError(w, err)
		return
	}
	write(w, http.StatusOK, merge)
}
func (s *Server) identityUnmerge(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	merge, err := s.catalog.UnmergeIdentity(r.PathValue("id"))
	if err != nil {
		s.identityError(w, err)
		return
	}
	write(w, http.StatusOK, merge)
}
func (s *Server) identityError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrIdentityConflict) {
		fail(w, http.StatusConflict, "identity_conflict")
		return
	}
	s.metadataError(w, err)
}
