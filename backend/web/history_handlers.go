package web

import (
	"errors"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"net/http"
	"strconv"
)

type ratingRequest struct {
	Value int `json:"value"`
}
type importHistoryRequest struct {
	Events []household.ViewingEvent `json:"events"`
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	p, _ := s.house.Profile(s.session(r))
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 100 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	cursor := r.URL.Query().Get("before")
	page := household.HistoryPage{Events: []household.ViewingEvent{}}
	for len(page.Events) < limit {
		candidate, err := s.house.History(p.ID, 1, cursor)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if len(candidate.Events) == 0 {
			break
		}
		cursor = candidate.Next
		if s.catalog.IsMediaVersionMember(candidate.Events[0].Kind, candidate.Events[0].CatalogID) {
			if cursor == "" {
				page.Next = ""
				break
			}
			continue
		}
		allowed, err := s.itemAllowed(r, candidate.Events[0].CatalogID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "catalog_query_failed")
			return
		}
		if allowed {
			page.Events = append(page.Events, candidate.Events[0])
			page.Next = cursor
		}
		if cursor == "" {
			page.Next = ""
			break
		}
	}
	write(w, http.StatusOK, page)
}
func (s *Server) rating(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	p, _ := s.house.Profile(s.session(r))
	id := r.PathValue("id")
	if !s.publicItem(w, r, id) {
		return
	}
	if r.Method == http.MethodGet {
		rating, ok, err := s.house.Rating(p.ID, id)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if !ok {
			write(w, http.StatusOK, map[string]any{"rating": nil})
			return
		}
		write(w, http.StatusOK, map[string]any{"rating": rating})
		return
	}
	if !s.sameOrigin(w, r) {
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.house.DeleteRating(p.ID, id); err != nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		write(w, http.StatusOK, map[string]bool{"deleted": true})
		return
	}
	var body ratingRequest
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.house.SetRating(p.ID, id, body.Value, household.ProvenanceLocal, ""); err != nil {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	write(w, http.StatusOK, map[string]bool{"saved": true})
}
func (s *Server) clearHistory(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.profile(w, r) {
		return
	}
	p, _ := s.house.Profile(s.session(r))
	clear, err := s.house.ClearHistory(p.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "history_failed")
		return
	}
	write(w, http.StatusOK, clear)
}
func (s *Server) undoClearHistory(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.profile(w, r) {
		return
	}
	p, _ := s.house.Profile(s.session(r))
	err := s.house.UndoClearHistory(p.ID, r.PathValue("id"))
	if errors.Is(err, household.ErrHistoryClearNotFound) {
		fail(w, http.StatusNotFound, "history_clear_not_found")
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	write(w, http.StatusOK, map[string]bool{"restored": true})
}
func (s *Server) importHistory(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.profile(w, r) {
		return
	}
	var body importHistoryRequest
	if !decode(r, &body) || len(body.Events) == 0 || len(body.Events) > 100 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	p, _ := s.house.Profile(s.session(r))
	for _, event := range body.Events {
		if !s.publicItem(w, r, event.CatalogID) {
			return
		}
		event.Provenance = household.ProvenanceImport
		if err := s.house.RecordViewingEvent(p.ID, event); err != nil {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	write(w, http.StatusOK, map[string]int{"imported": len(body.Events)})
}
