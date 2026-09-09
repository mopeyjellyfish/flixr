package web

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/mopeyjellyfish/flixr/backend/backup"
)

func (s *Server) backupStatus(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if s.backups == nil {
		fail(w, 503, "backups_unavailable")
		return
	}
	policy, err := s.backups.Policy()
	if err != nil {
		fail(w, 500, "backup_status_failed")
		return
	}
	jobs, err := s.backups.Jobs()
	if err != nil {
		fail(w, 500, "backup_status_failed")
		return
	}
	write(w, 200, map[string]any{"policy": policy, "jobs": jobs})
}
func (s *Server) backupPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if s.backups == nil {
		fail(w, 503, "backups_unavailable")
		return
	}
	var policy backup.Policy
	if !decode(r, &policy) {
		fail(w, 400, "invalid_backup_policy")
		return
	}
	saved, err := s.backups.SetPolicy(policy)
	if err != nil {
		fail(w, 400, "invalid_backup_policy")
		return
	}
	write(w, 200, map[string]any{"policy": saved})
}
func (s *Server) backupRun(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if s.backups == nil {
		fail(w, 503, "backups_unavailable")
		return
	}
	job, err := s.backups.Queue("manual")
	if err != nil {
		fail(w, 409, "backup_busy")
		return
	}
	write(w, 202, map[string]any{"job": job})
}
func (s *Server) backupCancel(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if s.backups == nil {
		fail(w, 503, "backups_unavailable")
		return
	}
	job, err := s.backups.Cancel(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "backup_job_not_found")
		return
	}
	if err != nil {
		fail(w, 500, "backup_cancel_failed")
		return
	}
	write(w, 200, map[string]any{"job": job})
}
