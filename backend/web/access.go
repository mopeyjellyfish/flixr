package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
)

func (s *Server) profileAccessPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	profileID := r.PathValue("id")
	if r.Method == http.MethodGet {
		policy, err := s.house.Policy(profileID)
		if errors.Is(err, household.ErrProfileNotFound) {
			fail(w, http.StatusNotFound, "profile_not_found")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "profile_policy_failed")
			return
		}
		write(w, http.StatusOK, policy)
		return
	}
	var policy access.Policy
	if !decode(r, &policy) {
		fail(w, http.StatusBadRequest, "invalid_profile_policy")
		return
	}
	updated, revoked, err := s.house.UpdatePolicy(profileID, policy)
	if errors.Is(err, access.ErrInvalidPolicy) {
		fail(w, http.StatusBadRequest, "invalid_profile_policy")
		return
	}
	if errors.Is(err, household.ErrProfileNotFound) {
		fail(w, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "profile_policy_failed")
		return
	}
	for _, viewerID := range revoked {
		s.playback.StopViewer(viewerID)
	}
	write(w, http.StatusOK, updated)
}

func (s *Server) playbackCatalogItem(ctx context.Context, r *http.Request, id string) (catalog.Item, error) {
	policy, ok := s.requestPolicy(r)
	if !ok {
		return catalog.Item{}, catalog.ErrAccessDenied
	}
	return s.catalog.PlaybackItemForPolicy(ctx, id, policy)
}

func (s *Server) requestPolicy(r *http.Request) (access.Policy, bool) {
	profile, ok := s.house.Profile(s.session(r))
	if !ok {
		return access.Policy{}, false
	}
	policy, err := s.house.Policy(profile.ID)
	return policy, err == nil
}

func (s *Server) contentAllowed(r *http.Request, kind, id string) (bool, error) {
	policy, ok := s.requestPolicy(r)
	if !ok {
		return false, nil
	}
	if !policy.Restricted() {
		return true, nil
	}
	content, found, err := s.catalog.AccessContent(kind, id)
	return found && policy.Allows(content), err
}

func (s *Server) itemAllowed(r *http.Request, id string) (bool, error) {
	policy, ok := s.requestPolicy(r)
	if !ok {
		return false, nil
	}
	if !policy.Restricted() {
		return true, nil
	}
	content, found, err := s.catalog.AccessContentForItem(id)
	return found && policy.Allows(content), err
}

func (s *Server) publicContent(w http.ResponseWriter, r *http.Request, kind, id string) bool {
	allowed, err := s.contentAllowed(r, kind, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "catalog_query_failed")
		return false
	}
	if !allowed {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return false
	}
	return true
}

func (s *Server) publicItem(w http.ResponseWriter, r *http.Request, id string) bool {
	allowed, err := s.itemAllowed(r, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "catalog_query_failed")
		return false
	}
	if !allowed {
		fail(w, http.StatusNotFound, "catalog_not_found")
		return false
	}
	return true
}

func (s *Server) playableItem(w http.ResponseWriter, r *http.Request, id string) bool {
	allowed, err := s.itemAllowed(r, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "playback_failed")
		return false
	}
	if !allowed {
		fail(w, http.StatusForbidden, "content_access_denied")
		return false
	}
	return true
}

func (s *Server) filterBrowse(r *http.Request, items []catalog.Item) ([]catalog.Item, error) {
	policy, ok := s.requestPolicy(r)
	if !ok || !policy.Restricted() {
		return items, nil
	}
	out := make([]catalog.Item, 0, len(items))
	for _, item := range items {
		content, found, err := s.catalog.AccessContent(item.Kind, item.ID)
		if err != nil {
			return nil, err
		}
		if found && policy.Allows(content) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *Server) filterViewer(r *http.Request, model catalog.ViewerModel) (catalog.ViewerModel, error) {
	filter := func(items []catalog.ViewerItem) ([]catalog.ViewerItem, error) {
		out := make([]catalog.ViewerItem, 0, len(items))
		for _, item := range items {
			allowed, err := s.contentAllowed(r, item.Kind, item.ID)
			if err != nil {
				return nil, err
			}
			if allowed {
				out = append(out, item)
			}
		}
		return out, nil
	}
	var err error
	model.Items, err = filter(model.Items)
	if err != nil {
		return catalog.ViewerModel{}, err
	}
	visibleSections := model.Sections[:0]
	for i := range model.Sections {
		model.Sections[i].Items, err = filter(model.Sections[i].Items)
		if err != nil {
			return catalog.ViewerModel{}, err
		}
		// Viewer always builds its three standard rows first. Empty genre rows
		// after them would disclose metadata from an otherwise hidden title.
		if i < 3 || len(model.Sections[i].Items) > 0 {
			visibleSections = append(visibleSections, model.Sections[i])
		}
	}
	model.Sections = visibleSections
	return model, nil
}
