// Package web exposes Flixr's versioned JSON HTTP API and frontend.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/diagnostics"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/screens"
)

type Readiness struct {
	FFprobe bool `json:"ffprobe"`
	FFmpeg  bool `json:"ffmpeg"`
}
type Build struct{ Version, Revision string }
type Server struct {
	house             *household.Manager
	catalog           *catalog.Catalog
	playback          *playback.Manager
	screens           *screens.Manager
	mux               *http.ServeMux
	lookPath          func(string) (string, error)
	readyMu           sync.RWMutex
	readiness         Readiness
	diagnostics       *diagnostics.Log
	version, revision string
	settingsLocks     map[string]bool
	settingsValues    map[string]string
}

func NewServer(h *household.Manager, c *catalog.Catalog) *Server {
	return NewServerWithPlayback(h, c, playback.NewDirectManager())
}

func NewServerWithPlayback(h *household.Manager, c *catalog.Catalog, manager *playback.Manager) *Server {
	return NewServerWithScreens(h, c, manager, screens.New(time.Minute))
}

func NewServerWithScreens(h *household.Manager, c *catalog.Catalog, playbackManager *playback.Manager, screenManager *screens.Manager, builds ...Build) *Server {
	build := Build{Version: "dev", Revision: "unknown"}
	if len(builds) > 0 {
		build = builds[0]
	}
	return newServer(h, c, playbackManager, screenManager, nil, build)
}

// NewServerWithConfiguration makes explicit environment values visible and
// immutable to owner settings. Existing constructors retain test-friendly
// defaults with no environment locks.
func NewServerWithConfiguration(h *household.Manager, c *catalog.Catalog, playbackManager *playback.Manager, locks map[string]bool) *Server {
	return newServer(h, c, playbackManager, screens.New(time.Minute), locks, Build{Version: "dev", Revision: "unknown"})
}

func NewServerWithConfigurationValues(h *household.Manager, c *catalog.Catalog, playbackManager *playback.Manager, screenManager *screens.Manager, locks map[string]bool, values map[string]string, builds ...Build) *Server {
	build := Build{Version: "dev", Revision: "unknown"}
	if len(builds) > 0 {
		build = builds[0]
	}
	s := newServer(h, c, playbackManager, screenManager, locks, build)
	s.settingsValues = values
	return s
}

func newServer(h *household.Manager, c *catalog.Catalog, playbackManager *playback.Manager, screenManager *screens.Manager, locks map[string]bool, build Build) *Server {
	if locks == nil {
		locks = map[string]bool{}
	}
	s := &Server{house: h, catalog: c, playback: playbackManager, screens: screenManager, mux: http.NewServeMux(), lookPath: exec.LookPath, diagnostics: diagnostics.New(100), version: build.Version, revision: build.Revision, settingsLocks: locks, settingsValues: map[string]string{}}
	s.checkReadiness()
	s.routes()
	return s
}
func (s *Server) Handler() http.Handler { return s.observe(s.mux) }
func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/setup/status", s.status)
	s.mux.HandleFunc("POST /api/v1/setup/claim", s.claim)
	s.mux.HandleFunc("POST /api/v1/owner/login", s.login)
	s.mux.HandleFunc("POST /api/v1/logout", s.logout)
	s.mux.HandleFunc("POST /api/v1/profiles", s.createProfile)
	s.mux.HandleFunc("GET /api/v1/profiles", s.listProfiles)
	s.mux.HandleFunc("PATCH /api/v1/profiles/{id}", s.updateProfile)
	s.mux.HandleFunc("DELETE /api/v1/profiles/{id}", s.deleteProfile)
	s.mux.HandleFunc("GET /api/v1/owner/sessions", s.sessions)
	s.mux.HandleFunc("DELETE /api/v1/owner/sessions/{id}", s.revokeSession)
	s.mux.HandleFunc("POST /api/v1/profiles/{id}/select", s.selectProfile)
	s.mux.HandleFunc("GET /api/v1/catalog/home", s.home)
	s.mux.HandleFunc("GET /api/v1/history", s.history)
	s.mux.HandleFunc("POST /api/v1/history/import", s.importHistory)
	s.mux.HandleFunc("POST /api/v1/history/clear", s.clearHistory)
	s.mux.HandleFunc("POST /api/v1/history/clear/{id}/undo", s.undoClearHistory)
	s.mux.HandleFunc("GET /api/v1/ratings/{id}", s.rating)
	s.mux.HandleFunc("PUT /api/v1/ratings/{id}", s.rating)
	s.mux.HandleFunc("DELETE /api/v1/ratings/{id}", s.rating)
	s.mux.HandleFunc("GET /api/v1/catalog/search", s.search)
	s.mux.HandleFunc("GET /api/v1/catalog/films/{id}", s.film)
	s.mux.HandleFunc("GET /api/v1/catalog/series/{id}", s.series)
	s.mux.HandleFunc("GET /api/v1/catalog/items/{id}", s.item)
	s.mux.HandleFunc("GET /api/v1/catalog/view", s.viewer)
	s.mux.HandleFunc("PUT /api/v1/catalog/watched/{kind}/{id}", s.watched)
	s.mux.HandleFunc("PUT /api/v1/catalog/list/{kind}/{id}", s.list)
	s.mux.HandleFunc("DELETE /api/v1/catalog/list/{kind}/{id}", s.list)
	s.mux.HandleFunc("GET /api/v1/catalog/preferences/{media}", s.preferences)
	s.mux.HandleFunc("PUT /api/v1/catalog/preferences/{media}", s.preferences)
	s.mux.HandleFunc("GET /api/v1/catalog/artwork/{id}/{kind}", s.artwork)
	s.mux.HandleFunc("PUT /api/v1/progress/{id}", s.progress)
	s.mux.HandleFunc("GET /api/v1/progress/{id}", s.progress)
	s.mux.HandleFunc("GET /api/v1/owner/roots", s.roots)
	s.mux.HandleFunc("POST /api/v1/owner/roots", s.roots)
	s.mux.HandleFunc("POST /api/v1/owner/scan", s.scan)
	s.mux.HandleFunc("GET /api/v1/owner/scan/status", s.scanStatus)
	s.mux.HandleFunc("GET /api/v1/owner/settings/tmdb", s.tmdbSettings)
	s.mux.HandleFunc("PUT /api/v1/owner/settings/tmdb", s.tmdbSettings)
	s.mux.HandleFunc("GET /api/v1/owner/identity/repairs", s.identityRepairs)
	s.mux.HandleFunc("POST /api/v1/owner/identity/merges", s.identityMerge)
	s.mux.HandleFunc("POST /api/v1/owner/identity/merges/{id}/unmerge", s.identityUnmerge)
	s.mux.HandleFunc("GET /api/v1/owner/metadata/unmatched", s.unmatchedMetadata)
	s.mux.HandleFunc("GET /api/v1/owner/metadata/{kind}/{id}/candidates", s.metadataCandidates)
	s.mux.HandleFunc("PUT /api/v1/owner/metadata/{kind}/{id}/match", s.metadataMatch)
	s.mux.HandleFunc("DELETE /api/v1/owner/metadata/{kind}/{id}/match", s.metadataUnmatch)
	s.mux.HandleFunc("GET /api/v1/owner/settings", s.settingsInventory)
	s.mux.HandleFunc("GET /api/v1/owner/settings/export", s.settingsExport)
	s.mux.HandleFunc("POST /api/v1/owner/settings/import/preview", s.settingsImportPreview)
	s.mux.HandleFunc("POST /api/v1/owner/settings/import", s.settingsImport)
	s.mux.HandleFunc("GET /api/v1/owner/metadata/{kind}/{id}/fields", s.metadataFields)
	s.mux.HandleFunc("POST /api/v1/owner/metadata/{kind}/{id}/preview", s.metadataPreview)
	s.mux.HandleFunc("PUT /api/v1/owner/metadata/{kind}/{id}/fields", s.metadataEdit)
	s.mux.HandleFunc("POST /api/v1/owner/metadata/{kind}/{id}/refresh/preview", s.metadataRefreshPreview)
	s.mux.HandleFunc("POST /api/v1/owner/metadata/{kind}/{id}/refresh", s.metadataRefresh)
	s.mux.HandleFunc("POST /api/v1/owner/readiness/recheck", s.recheckReadiness)
	s.mux.HandleFunc("POST /api/v1/playback/plans", s.playbackPlan)
	s.mux.HandleFunc("GET /api/v1/playback/sessions/{id}/media", s.playbackMedia)
	s.mux.HandleFunc("GET /api/v1/playback/sessions/{id}/next", s.playbackNext)
	s.mux.HandleFunc("POST /api/v1/playback/sessions/{id}/stop", s.playbackStop)
	s.mux.HandleFunc("GET /api/v1/playback/sessions/{id}/manifest.m3u8", s.playbackManifest)
	s.mux.HandleFunc("GET /api/v1/playback/sessions/{id}/{name}", s.playbackSegment)
	s.mux.HandleFunc("POST /api/v1/playback/sessions/{id}/heartbeat", s.playbackHeartbeat)
	s.mux.HandleFunc("POST /api/v1/playback/sessions/{id}/seek", s.playbackSeek)
	s.mux.HandleFunc("GET /api/v1/playback/input/{token}", s.playbackInput)
	s.mux.HandleFunc("GET /api/v1/owner/settings/playback", s.playbackSettings)
	s.mux.HandleFunc("PUT /api/v1/owner/settings/playback", s.playbackSettings)
	s.mux.HandleFunc("GET /api/v1/owner/playback/status", s.playbackStatus)
	s.mux.HandleFunc("GET /api/v1/owner/diagnostics", s.exportDiagnostics)
	s.mux.HandleFunc("GET /api/v1/screens", s.listScreens)
	s.mux.HandleFunc("POST /api/v1/screens/presence", s.advertiseScreen)
	s.mux.HandleFunc("POST /api/v1/screens/{id}/sessions", s.authorizeScreen)
	s.mux.HandleFunc("GET /api/v1/screens/receiver", s.screenReceiver)
	s.mux.HandleFunc("GET /api/v1/screens/control", s.screenControl)
	s.mux.HandleFunc("GET /api/v1/owner/screens", s.ownerScreens)
	s.mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, _ *http.Request) { fail(w, http.StatusNotFound, "not_found") })
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { fail(w, http.StatusNotFound, "not_found") })
	s.mux.Handle("/", frontendHandler())
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code}})
}
func decode(r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return false
	}
	return d.Decode(&struct{}{}) == io.EOF
}
func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	s.readyMu.RLock()
	ready := s.readiness
	s.readyMu.RUnlock()
	write(w, 200, map[string]any{"claimed": s.house.Claimed(), "readiness": ready, "demo": s.catalog.Demo(), "demo_source": s.catalog.DemoSource()})
}
func (s *Server) checkReadiness() {
	_, p := s.lookPath("ffprobe")
	_, f := s.lookPath("ffmpeg")
	s.readyMu.Lock()
	s.readiness = Readiness{p == nil, f == nil}
	s.readyMu.Unlock()
}
func (s *Server) recheckReadiness(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	s.checkReadiness()
	s.status(w, r)
}
func (s *Server) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	if origin := r.Header.Get("Origin"); origin == "" || origin == scheme(r)+r.Host {
		return true
	}
	fail(w, 403, "bad_origin")
	return false
}
func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var v struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(r, &v) {
		fail(w, 400, "invalid_request")
		return
	}
	x, e := s.house.Claim(v.Token, v.Password)
	if e != nil {
		if errors.Is(e, household.ErrHashSaturated) {
			fail(w, 429, "credential_busy")
		} else if errors.Is(e, household.ErrClaimed) {
			fail(w, 409, "already_claimed")
		} else {
			fail(w, 401, "invalid_token")
		}
		return
	}
	s.cookie(w, r, x)
	write(w, 201, map[string]bool{"claimed": true})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var v struct {
		Password string `json:"password"`
	}
	if !decode(r, &v) {
		fail(w, 400, "invalid_request")
		return
	}
	x, e := s.house.Login(v.Password)
	if e != nil {
		if errors.Is(e, household.ErrHashSaturated) {
			fail(w, 429, "credential_busy")
		} else if errors.Is(e, household.ErrRateLimited) {
			fail(w, 429, "login_rate_limited")
		} else {
			fail(w, 401, "invalid_credentials")
		}
		return
	}
	s.cookie(w, r, x)
	write(w, 200, map[string]bool{"logged_in": true})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	session := s.session(r)
	viewerID, _ := s.house.SessionIdentity(session)
	if err := s.house.Logout(session); err != nil {
		fail(w, 500, "logout_failed")
		return
	}
	s.playback.StopViewer(viewerID)
	http.SetCookie(w, &http.Cookie{Name: "flixr_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1})
	write(w, 200, map[string]bool{"logged_out": true})
}
func (s *Server) cookie(w http.ResponseWriter, r *http.Request, x string) {
	http.SetCookie(w, &http.Cookie{Name: "flixr_session", Value: x, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 86400})
}
func (s *Server) session(r *http.Request) string {
	c, e := r.Cookie("flixr_session")
	if e != nil {
		return ""
	}
	return c.Value
}
func (s *Server) owner(w http.ResponseWriter, r *http.Request) bool {
	if !s.sameOrigin(w, r) {
		return false
	}
	if !s.house.Owner(s.session(r)) {
		fail(w, 403, "owner_required")
		return false
	}
	return true
}
func (s *Server) profile(w http.ResponseWriter, r *http.Request) bool {
	if !s.sameOrigin(w, r) {
		return false
	}
	if _, ok := s.house.Profile(s.session(r)); !ok {
		fail(w, 403, "profile_required")
		return false
	}
	return true
}
func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https://"
	}
	return "http://"
}
func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var v struct {
		Name string `json:"name"`
		PIN  string `json:"pin"`
	}
	if !decode(r, &v) || strings.TrimSpace(v.Name) == "" {
		fail(w, 400, "invalid_request")
		return
	}
	p, e := s.house.CreateProfile(v.Name, v.PIN)
	if e != nil {
		if errors.Is(e, household.ErrHashSaturated) {
			fail(w, 429, "credential_busy")
		} else {
			fail(w, 500, "profile_failed")
		}
		return
	}
	write(w, 201, p)
}
func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"profiles": s.house.Profiles()})
}
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	var v struct {
		Name      string `json:"name"`
		PIN       string `json:"pin"`
		Unprotect bool   `json:"unprotect"`
	}
	if !decode(r, &v) {
		fail(w, 400, "invalid_request")
		return
	}
	p, e := s.house.UpdateProfile(r.PathValue("id"), v.Name, v.PIN, v.Unprotect)
	if e != nil {
		if errors.Is(e, household.ErrHashSaturated) {
			fail(w, 429, "credential_busy")
		} else if errors.Is(e, household.ErrProfileNotFound) {
			fail(w, 404, "profile_not_found")
		} else {
			fail(w, 500, "profile_failed")
		}
		return
	}
	write(w, 200, p)
}
func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if err := s.house.DeleteProfile(r.PathValue("id")); err != nil {
		if errors.Is(err, household.ErrProfileNotFound) {
			fail(w, 404, "profile_not_found")
		} else {
			fail(w, 500, "profile_failed")
		}
		return
	}
	write(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	sessions, err := s.house.ActiveSessions()
	if err != nil {
		fail(w, 500, "session_failed")
		return
	}
	write(w, 200, map[string]any{"sessions": sessions})
}
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if err := s.house.RevokeSession(r.PathValue("id")); err != nil {
		if errors.Is(err, household.ErrSessionNotFound) {
			fail(w, 404, "session_not_found")
		} else {
			fail(w, 500, "session_failed")
		}
		return
	}
	s.playback.StopViewer(r.PathValue("id"))
	write(w, 200, map[string]bool{"revoked": true})
}
func (s *Server) selectProfile(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var v struct {
		PIN string `json:"pin"`
	}
	if !decode(r, &v) {
		fail(w, 400, "invalid_request")
		return
	}
	priorViewerID, _ := s.house.SessionIdentity(s.session(r))
	x, e := s.house.Select(r.PathValue("id"), v.PIN)
	if e != nil {
		if errors.Is(e, household.ErrHashSaturated) {
			fail(w, 429, "credential_busy")
		} else if errors.Is(e, household.ErrRateLimited) {
			fail(w, 429, "pin_rate_limited")
		} else {
			fail(w, 401, "invalid_pin")
		}
		return
	}
	s.playback.StopViewer(priorViewerID)
	s.cookie(w, r, x)
	write(w, 200, map[string]bool{"selected": true})
}
func pagination(r *http.Request) (int, int, bool) {
	o, l := 0, 50
	if x := r.URL.Query().Get("offset"); x != "" {
		var e error
		o, e = strconv.Atoi(x)
		if e != nil || o < 0 {
			return 0, 0, false
		}
	}
	if x := r.URL.Query().Get("limit"); x != "" {
		var e error
		l, e = strconv.Atoi(x)
		if e != nil || l < 1 || l > 100 {
			return 0, 0, false
		}
	}

	return o, l, true
}

func nextPage(offset, limit, total int) any {
	if offset+limit >= total {
		return nil
	}
	return offset + limit
}
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	o, l, ok := pagination(r)
	if !ok {
		fail(w, 400, "invalid_pagination")
		return
	}
	items, total, err := s.catalog.Browse("", o, l)
	if err != nil {
		fail(w, 500, "catalog_query_failed")
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "next": nextPage(o, l, total)})
}
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	o, l, ok := pagination(r)
	if !ok {
		fail(w, 400, "invalid_pagination")
		return
	}
	items, total, err := s.catalog.Browse(r.URL.Query().Get("q"), o, l)
	if err != nil {
		fail(w, 500, "catalog_query_failed")
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "next": nextPage(o, l, total)})
}
func (s *Server) film(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	v, ok := s.catalog.Item(r.PathValue("id"))
	if !ok || v.Kind != "film" {
		fail(w, 404, "catalog_not_found")
		return
	}
	write(w, http.StatusOK, v)
}
func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	v, ok := s.catalog.Series(r.PathValue("id"))
	if !ok {
		fail(w, 404, "catalog_not_found")
		return
	}
	write(w, http.StatusOK, v)
}
func (s *Server) item(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	v, ok := s.catalog.Item(r.PathValue("id"))
	if !ok {
		fail(w, 404, "catalog_not_found")
		return
	}
	write(w, 200, v)
}
func (s *Server) artwork(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("w"))
	data, contentType, err := s.catalog.ArtworkSized(r.Context(), r.PathValue("id"), r.PathValue("kind"), width)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if err != nil {
		fail(w, http.StatusNotFound, "catalog_artwork_not_found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Artwork is refreshed at a stable catalog URL, so a browser must revalidate it.
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	if !s.profile(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		p, generation, e := s.house.ProgressState(s.session(r), r.PathValue("id"))
		if e != nil {
			fail(w, 500, "progress_failed")
			return
		}
		write(w, 200, map[string]int64{"position_ms": p, "generation": generation})
		return
	}
	var v struct {
		Generation int64 `json:"generation"`
		Position   int64 `json:"position_ms"`
		ObservedAt int64 `json:"observed_at"`
	}
	if !decode(r, &v) || v.Position < 0 {
		fail(w, 400, "invalid_request")
		return
	}
	profile, _ := s.house.Profile(s.session(r))
	if e := s.house.RecordProgress(profile.ID, r.PathValue("id"), v.Position, v.ObservedAt, false, v.Generation); e != nil {
		if errors.Is(e, household.ErrProgressConflict) {
			fail(w, http.StatusConflict, "progress_conflict")
			return
		}
		fail(w, 500, "progress_failed")
		return
	}
	write(w, 200, map[string]bool{"saved": true})
}
func (s *Server) roots(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		films, tv := s.catalog.Roots()
		write(w, http.StatusOK, map[string]string{"films": films, "tv": tv})
		return
	}
	var v struct {
		Films string `json:"films"`
		TV    string `json:"tv"`
	}
	if !decode(r, &v) {
		fail(w, 400, "invalid_roots")
		return
	}
	films, tv := s.catalog.Roots()
	if (s.settingsLocks["library.films_root"] && v.Films != films) || (s.settingsLocks["library.tv_root"] && v.TV != tv) {
		fail(w, http.StatusConflict, "environment_locked")
		return
	}
	if s.catalog.SetRoots(v.Films, v.TV) != nil {
		fail(w, 400, "invalid_roots")
		return
	}
	write(w, 200, map[string]bool{"saved": true})
}
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	s.readyMu.RLock()
	ffprobe := s.readiness.FFprobe
	s.readyMu.RUnlock()
	if !ffprobe {
		fail(w, http.StatusServiceUnavailable, "ffprobe_unavailable")
		return
	}
	if err := s.catalog.StartScan(context.Background(), 2); err != nil {
		if errors.Is(err, catalog.ErrScanActive) {
			fail(w, 409, "scan_active")
		} else {
			fail(w, 500, "scan_failed")
		}
		return
	}
	write(w, http.StatusAccepted, map[string]any{"scan": s.catalog.ScanStatus()})
}

func (s *Server) scanStatus(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	write(w, http.StatusOK, map[string]any{"scan": s.catalog.ScanStatus()})
}

func (s *Server) tmdbSettings(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		write(w, http.StatusOK, map[string]bool{"configured": s.catalog.TMDBConfigured()})
		return
	}
	var v struct {
		Token string `json:"token"`
	}
	if s.settingsLocks["metadata.tmdb_token"] {
		fail(w, http.StatusConflict, "environment_locked")
		return
	}
	if !decode(r, &v) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.catalog.SetTMDBToken(strings.TrimSpace(v.Token)); err != nil {
		fail(w, http.StatusInternalServerError, "settings_failed")
		return
	}
	write(w, http.StatusOK, map[string]bool{"configured": s.catalog.TMDBConfigured()})
}
