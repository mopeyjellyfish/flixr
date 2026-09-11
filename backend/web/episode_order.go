package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func (s *Server) episodeOrders(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 || limit < 0 || limit > 50 {
		fail(w, 400, "episode_order_invalid")
		return
	}
	if limit == 0 {
		limit = 50
	}
	series, total := s.catalog.EpisodeOrderSeries(r.URL.Query().Get("q"), offset, limit)
	out := map[string]any{"series": series, "total": total}
	if offset+len(series) < total {
		out["next_offset"] = offset + len(series)
	}
	write(w, 200, out)
}

func (s *Server) episodeOrder(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodGet {
		detail, err := s.catalog.EpisodeOrder(id)
		s.episodeOrderError(w, err)
		if err == nil {
			write(w, 200, detail)
		}
		return
	}
	var body struct {
		Order    string                      `json:"order"`
		Revision int                         `json:"revision"`
		Entries  []catalog.EpisodeOrderEntry `json:"entries"`
	}
	if !decode(r, &body) {
		fail(w, 400, "episode_order_invalid")
		return
	}
	detail, err := s.catalog.SaveEpisodeOrder(r.Context(), id, body.Order, body.Revision, body.Entries)
	s.episodeOrderError(w, err)
	if err == nil {
		write(w, 200, detail)
	}
}

func (s *Server) episodeOrderGroups(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	groups, err := s.catalog.EpisodeOrderGroups(r.Context(), r.PathValue("id"))
	s.episodeOrderProviderError(w, err)
	if err == nil {
		write(w, http.StatusOK, map[string]any{"groups": groups})
	}
}
func (s *Server) episodeOrderPreview(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var body struct {
		GroupID string `json:"group_id"`
	}
	if !decode(r, &body) || body.GroupID == "" {
		fail(w, 400, "episode_order_invalid")
		return
	}
	detail, err := s.catalog.PreviewEpisodeOrder(r.Context(), r.PathValue("id"), body.GroupID)
	s.episodeOrderProviderError(w, err)
	if err == nil {
		write(w, http.StatusOK, detail)
	}
}
func (s *Server) episodeOrderProviderError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, catalog.ErrCatalogNotFound) || errors.Is(err, catalog.ErrEpisodeOrderInvalid) || errors.Is(err, catalog.ErrEpisodeOrderConflict) {
		s.episodeOrderError(w, err)
		return
	}
	fail(w, http.StatusServiceUnavailable, "metadata_unavailable")
}
func (s *Server) episodeOrderError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, catalog.ErrCatalogNotFound):
		fail(w, 404, "catalog_not_found")
	case errors.Is(err, catalog.ErrEpisodeOrderConflict):
		fail(w, 409, "episode_order_conflict")
	default:
		fail(w, 400, "episode_order_invalid")
	}
}
