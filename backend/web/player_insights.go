package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/preview"
)

func (s *Server) playerInsights(w http.ResponseWriter, r *http.Request) {
	session, ok := s.playbackSession(w, r, false)
	if !ok {
		return
	}
	// Share the existing session lifetime cancellation mechanism used by captions.
	ctx, release, err := s.playback.SubtitleContext(r.Context(), session.ID, session.ViewerID, session.ProfileID)
	if err != nil {
		playbackFailure(w, err)
		return
	}
	defer release()
	file, err := s.catalog.OpenSource(session.CatalogID, session.Plan.SourceKey)
	if err != nil {
		fail(w, http.StatusNotFound, "playback_asset_not_found")
		return
	}
	defer file.Close()
	key := session.CatalogID + ":" + session.Plan.SourceKey
	var result []byte
	var chapters []preview.Chapter
	isFrame := strings.HasSuffix(r.URL.Path, "/preview.jpg")
	if isFrame {
		item, itemErr := s.catalog.PlaybackItem(session.CatalogID)
		if itemErr != nil {
			fail(w, http.StatusNotFound, "catalog_not_found")
			return
		}
		position, parseErr := strconv.ParseInt(r.URL.Query().Get("position_ms"), 10, 64)
		if parseErr != nil || position < 0 || position >= item.DurationMS {
			fail(w, http.StatusBadRequest, "invalid_request")
			return
		}
		result, err = s.previews.Frame(ctx, file, key, position)
	} else {
		chapters, err = s.previews.Chapters(ctx, file, key)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, preview.ErrBusy) {
			w.Header().Set("Retry-After", "1")
			fail(w, http.StatusServiceUnavailable, "playback_preparing")
			return
		}
		fail(w, http.StatusNotFound, "playback_asset_not_found")
		return
	}
	if ctx.Err() != nil {
		return
	}
	if _, ok := s.playback.LookupForViewer(session.ID, session.ViewerID, session.ProfileID, false); !ok {
		fail(w, http.StatusForbidden, "playback_session_invalid")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if !isFrame {
		write(w, http.StatusOK, map[string]any{"chapters": chapters})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}
