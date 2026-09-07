package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"runtime"
	"strconv"

	"github.com/mopeyjellyfish/flixr/backend/diagnostics"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		id := hex.EncodeToString(random[:])
		w.Header().Set("X-Flixr-Error-ID", id)
		captured := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(captured, r)
		if captured.status >= http.StatusBadRequest {
			s.diagnostics.RecordID("http_"+http.StatusText(captured.status), id)
		}
	})
}

func (s *Server) exportDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	s.readyMu.RLock()
	ready := s.readiness
	s.readyMu.RUnlock()
	scan := s.catalog.ScanStatus()
	status := s.playback.Status()
	archive, err := diagnostics.Build(diagnostics.Health{
		Version: s.version, Revision: s.revision, Runtime: runtime.Version(), Claimed: s.house.Claimed(),
		FFprobe: ready.FFprobe, FFmpeg: ready.FFmpeg, ScanStatus: scan.Status, ScanFailed: scan.Failed,
		ActiveGenerations: len(status.Generations),
	}, s.diagnostics.Records())
	if err != nil {
		fail(w, http.StatusInternalServerError, "diagnostics_failed")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="flixr-diagnostics.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive)
}
