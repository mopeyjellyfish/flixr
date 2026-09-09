package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/schedule"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

type Policy struct {
	Enabled          bool   `json:"enabled"`
	Destination      string `json:"destination"`
	ScheduleKind     string `json:"schedule_kind"`
	IntervalSeconds  int64  `json:"interval_seconds"`
	LocalTime        string `json:"local_time"`
	Timezone         string `json:"timezone"`
	RetainCount      int    `json:"retain_count"`
	RetainAgeSeconds int64  `json:"retain_age_seconds"`
	BudgetBytes      int64  `json:"budget_bytes"`
	NextRunAt        int64  `json:"next_run_at,omitempty"`
	LastVerifiedAt   int64  `json:"last_verified_at,omitempty"`
	LastVerifiedFile string `json:"last_verified_file,omitempty"`
	LastStatus       string `json:"last_status"`
	LastMessage      string `json:"last_message,omitempty"`
}

type Job struct {
	ID          string `json:"id"`
	Trigger     string `json:"trigger"`
	Status      string `json:"status"`
	QueuedAt    int64  `json:"queued_at"`
	StartedAt   int64  `json:"started_at,omitempty"`
	FinishedAt  int64  `json:"finished_at,omitempty"`
	ArchiveName string `json:"archive_name,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	Message     string `json:"message,omitempty"`
}

type Manager struct {
	db            *sqlite.DB
	source        Source
	mu            sync.Mutex
	cancel        context.CancelFunc
	stop          context.CancelFunc
	wake          chan struct{}
	done          chan struct{}
	running       bool
	now           func() time.Time
	override      *Policy
	locked        map[string]bool
	afterSelect   func(string)
	beforeArtwork func(string)
}

func NewManager(db *sqlite.DB, source Source) (*Manager, error) {
	if db == nil || source.DB == nil {
		return nil, errors.New("backup manager source is incomplete")
	}
	m := &Manager{db: db, source: source, wake: make(chan struct{}, 1), done: make(chan struct{}), now: time.Now}
	result, err := db.Exec(`UPDATE backup_jobs SET status='interrupted',finished_at=?,message='Interrupted by server restart.' WHERE status IN ('queued','running')`, m.now().Unix())
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed > 0 {
		_, _ = db.Exec(`UPDATE backup_policy SET last_status='interrupted',last_message='A backup was interrupted by server restart.' WHERE id=1`)
	}
	if err := m.cleanupPartials(); err != nil {
		return nil, errors.New("reconcile interrupted backup partials")
	}
	return m, nil
}

func (m *Manager) Policy() (Policy, error) {
	p, err := m.storedPolicy()
	if err != nil {
		return Policy{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.override != nil {
		applyLockedPolicy(&p, *m.override, m.locked)
	}
	return p, nil
}

func (m *Manager) storedPolicy() (Policy, error) {
	var p Policy
	var next, last sql.NullInt64
	err := m.db.QueryRow(`SELECT enabled,destination,schedule_kind,interval_seconds,local_time,timezone,retain_count,retain_age_seconds,budget_bytes,next_run_at,last_verified_at,last_verified_file,last_status,last_message FROM backup_policy WHERE id=1`).Scan(&p.Enabled, &p.Destination, &p.ScheduleKind, &p.IntervalSeconds, &p.LocalTime, &p.Timezone, &p.RetainCount, &p.RetainAgeSeconds, &p.BudgetBytes, &next, &last, &p.LastVerifiedFile, &p.LastStatus, &p.LastMessage)
	if next.Valid {
		p.NextRunAt = next.Int64
	}
	if last.Valid {
		p.LastVerifiedAt = last.Int64
	}
	return p, err
}

func (m *Manager) SetEnvironmentOverride(p Policy, locked map[string]bool) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	if p.Destination != "" {
		if _, err := authorizeDestination(p.Destination, m.excludedRoots()); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.override = &p
	m.locked = locked
	m.mu.Unlock()
	if locked["backup.schedule"] {
		next := int64(0)
		if p.Enabled {
			p.NextRunAt = 0
			next = nextRun(m.now(), p).Unix()
		}
		if _, err := m.db.Exec(`UPDATE backup_policy SET next_run_at=? WHERE id=1`, nullableInt(next)); err != nil {
			return err
		}
	}
	if err := m.cleanupPartials(); err != nil {
		return errors.New("reconcile interrupted backup partials")
	}
	m.signal()
	return nil
}

func applyLockedPolicy(target *Policy, source Policy, locked map[string]bool) {
	if locked["backup.destination"] {
		target.Destination = source.Destination
	}
	if locked["backup.schedule"] {
		target.Enabled = source.Enabled
		target.ScheduleKind = source.ScheduleKind
		target.IntervalSeconds = source.IntervalSeconds
		target.LocalTime = source.LocalTime
		target.Timezone = source.Timezone
	}
	if locked["backup.retain_count"] {
		target.RetainCount = source.RetainCount
	}
	if locked["backup.retain_age"] {
		target.RetainAgeSeconds = source.RetainAgeSeconds
	}
	if locked["backup.budget_bytes"] {
		target.BudgetBytes = source.BudgetBytes
	}
}

func validatePolicy(p Policy) error {
	if p.Destination != "" && !filepath.IsAbs(p.Destination) {
		return errors.New("backup destination must be absolute")
	}
	if p.Enabled && p.Destination == "" {
		return errors.New("an enabled backup policy requires a destination")
	}
	if p.ScheduleKind != "interval" && p.ScheduleKind != "daily" {
		return errors.New("invalid backup schedule kind")
	}
	if p.IntervalSeconds < 3600 || p.IntervalSeconds > 31536000 {
		return errors.New("backup interval must be between one hour and one year")
	}
	if _, err := time.Parse("15:04", p.LocalTime); err != nil {
		return errors.New("invalid backup local time")
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return errors.New("invalid backup timezone")
	}
	if p.RetainCount < 1 || p.RetainCount > 100 || p.RetainAgeSeconds < 86400 || p.BudgetBytes < 1048576 {
		return errors.New("invalid backup retention limits")
	}
	return nil
}

func ApplySchedule(p Policy, spec string) (Policy, error) {
	switch {
	case spec == "":
		return p, nil
	case spec == "off":
		p.Enabled = false
	case strings.HasPrefix(spec, "every:"):
		d, err := time.ParseDuration(strings.TrimPrefix(spec, "every:"))
		if err != nil {
			return p, errors.New("invalid backup schedule")
		}
		p.Enabled = true
		p.ScheduleKind = "interval"
		p.IntervalSeconds = int64(d / time.Second)
	case strings.HasPrefix(spec, "daily:"):
		parts := strings.SplitN(strings.TrimPrefix(spec, "daily:"), "@", 2)
		if len(parts) != 2 {
			return p, errors.New("invalid backup schedule")
		}
		p.Enabled = true
		p.ScheduleKind = "daily"
		p.LocalTime = parts[0]
		p.Timezone = parts[1]
	default:
		return p, errors.New("invalid backup schedule")
	}
	return p, validatePolicy(p)
}

func (m *Manager) SetPolicy(p Policy) (Policy, error) {
	if err := validatePolicy(p); err != nil {
		return Policy{}, err
	}
	stored, err := m.storedPolicy()
	if err != nil {
		return Policy{}, err
	}
	m.mu.Lock()
	locked, override := m.locked, m.override
	m.mu.Unlock()
	if override != nil {
		if locked["backup.destination"] {
			p.Destination = stored.Destination
		}
		if locked["backup.schedule"] {
			p.Enabled = stored.Enabled
			p.ScheduleKind = stored.ScheduleKind
			p.IntervalSeconds = stored.IntervalSeconds
			p.LocalTime = stored.LocalTime
			p.Timezone = stored.Timezone
		}
		if locked["backup.retain_count"] {
			p.RetainCount = stored.RetainCount
		}
		if locked["backup.retain_age"] {
			p.RetainAgeSeconds = stored.RetainAgeSeconds
		}
		if locked["backup.budget_bytes"] {
			p.BudgetBytes = stored.BudgetBytes
		}
	}
	effective := p
	if override != nil {
		applyLockedPolicy(&effective, *override, locked)
	}
	if effective.Destination != "" {
		if _, err := authorizeDestination(effective.Destination, m.excludedRoots()); err != nil {
			return Policy{}, err
		}
	}
	next := int64(0)
	if effective.Enabled {
		next = nextRun(m.now(), effective).Unix()
	}
	_, err = m.db.Exec(`UPDATE backup_policy SET enabled=?,destination=?,schedule_kind=?,interval_seconds=?,local_time=?,timezone=?,retain_count=?,retain_age_seconds=?,budget_bytes=?,next_run_at=?,updated_at=? WHERE id=1`, p.Enabled, p.Destination, p.ScheduleKind, p.IntervalSeconds, p.LocalTime, p.Timezone, p.RetainCount, p.RetainAgeSeconds, p.BudgetBytes, nullableInt(next), m.now().Unix())
	if err != nil {
		return Policy{}, err
	}
	m.signal()
	return m.Policy()
}

func (m *Manager) excludedRoots() []string {
	media := m.source.MediaRoots
	if m.source.MediaRootProvider != nil {
		if current, err := m.source.MediaRootProvider(); err == nil {
			media = current
		} else {
			return []string{m.source.DataDir, m.source.SegmentDir, "/"}
		}
	}
	return append([]string{m.source.DataDir, m.source.SegmentDir}, media...)
}

func (m *Manager) Queue(trigger string) (Job, error) {
	if trigger != "manual" && trigger != "schedule" {
		return Job{}, errors.New("invalid backup trigger")
	}
	p, err := m.Policy()
	if err != nil {
		return Job{}, err
	}
	if p.Destination == "" {
		return Job{}, errors.New("backup destination is not configured")
	}
	id, err := backupID()
	if err != nil {
		return Job{}, err
	}
	now := m.now().Unix()
	_, err = m.db.Exec(`INSERT INTO backup_jobs(id,trigger,status,queued_at) VALUES(?,?,'queued',?)`, id, trigger, now)
	if err != nil {
		return Job{}, errors.New("a backup is already queued or running")
	}
	m.signal()
	return m.Job(id)
}

func (m *Manager) Job(id string) (Job, error) {
	var j Job
	var started, finished sql.NullInt64
	err := m.db.QueryRow(`SELECT id,trigger,status,queued_at,started_at,finished_at,archive_name,size_bytes,message FROM backup_jobs WHERE id=?`, id).Scan(&j.ID, &j.Trigger, &j.Status, &j.QueuedAt, &started, &finished, &j.ArchiveName, &j.SizeBytes, &j.Message)
	if started.Valid {
		j.StartedAt = started.Int64
	}
	if finished.Valid {
		j.FinishedAt = finished.Int64
	}
	return j, err
}
func (m *Manager) Jobs() (jobs []Job, err error) {
	rows, err := m.db.Query(`SELECT id,trigger,status,queued_at,COALESCE(started_at,0),COALESCE(finished_at,0),archive_name,size_bytes,message FROM backup_jobs ORDER BY queued_at DESC,id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.Trigger, &j.Status, &j.QueuedAt, &j.StartedAt, &j.FinishedAt, &j.ArchiveName, &j.SizeBytes, &j.Message); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (m *Manager) Cancel(id string) (Job, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow(`SELECT status FROM backup_jobs WHERE id=? AND status IN ('queued','running')`, id).Scan(&status); err != nil {
		return Job{}, err
	}
	if _, err = tx.Exec(`UPDATE backup_jobs SET cancel_requested=1,status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END,finished_at=CASE WHEN status='queued' THEN ? ELSE finished_at END,message=CASE WHEN status='queued' THEN 'Cancelled by owner.' ELSE message END WHERE id=?`, m.now().Unix(), id); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	if status == "running" {
		m.mu.Lock()
		cancel := m.cancel
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return m.Job(id)
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	runCtx, stop := context.WithCancel(ctx)
	m.stop = stop
	m.mu.Unlock()
	go m.loop(runCtx)
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	running := m.running
	cancel := m.cancel
	stop := m.stop
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	if cancel != nil {
		cancel()
	}
	if !running {
		return nil
	}
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) loop(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		m.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
	}
}

func (m *Manager) runOnce(parent context.Context) {
	if parent.Err() != nil {
		return
	}
	now := m.now()
	p, err := m.Policy()
	if err != nil {
		return
	}
	if p.Enabled && p.NextRunAt > 0 && p.NextRunAt <= now.Unix() {
		_, _ = m.Queue("schedule")
		next := nextRun(now, p).Unix()
		_, _ = m.db.Exec(`UPDATE backup_policy SET next_run_at=? WHERE id=1`, next)
	}
	var id string
	if err := m.db.QueryRow(`SELECT id FROM backup_jobs WHERE status='queued' ORDER BY queued_at,id LIMIT 1`).Scan(&id); err != nil {
		return
	}
	if m.afterSelect != nil {
		m.afterSelect(id)
	}
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); m.cancel = nil; m.mu.Unlock() }()
	started := m.now().Unix()
	claimed, err := m.claimJob(id, started)
	if err != nil || !claimed {
		return
	}
	p, err = m.Policy()
	var result Result
	if err == nil {
		result, err = Create(ctx, m.source, Options{Destination: p.Destination, Now: m.now, beforeArtwork: m.beforeArtwork})
	}
	finished := m.now().Unix()
	status, message := "succeeded", ""
	if err != nil {
		status = "failed"
		message = backupFailureMessage(err)
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
			message = "Cancelled by owner."
			if parent.Err() != nil {
				status = "interrupted"
				message = "Interrupted by server shutdown."
			}
		}
	}
	name := ""
	if result.Path != "" {
		name = filepath.Base(result.Path)
	}
	_, _ = m.db.Exec(`UPDATE backup_jobs SET status=?,finished_at=?,archive_name=?,size_bytes=?,message=? WHERE id=?`, status, finished, name, result.Size, message, id)
	if err == nil {
		if rotateErr := Rotate(parent, p.Destination, Retention{p.RetainCount, time.Duration(p.RetainAgeSeconds) * time.Second, p.BudgetBytes}, m.now()); rotateErr != nil {
			message = "Backup verified; retention cleanup failed."
		}
		next := int64(0)
		if p.Enabled {
			next = nextRun(m.now(), p).Unix()
		}
		_, _ = m.db.Exec(`UPDATE backup_policy SET last_verified_at=?,last_verified_file=?,last_status='succeeded',last_message=?,next_run_at=? WHERE id=1`, finished, name, message, nullableInt(next))
	} else {
		_, _ = m.db.Exec(`UPDATE backup_policy SET last_status=?,last_message=? WHERE id=1`, status, message)
	}
}

func (m *Manager) claimJob(id string, started int64) (bool, error) {
	result, err := m.db.Exec(`UPDATE backup_jobs SET status='running',started_at=? WHERE id=? AND status='queued'`, started, id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func backupFailureMessage(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "space"):
		return "Backup destination does not have enough available space."
	case strings.Contains(text, "destination"):
		return "Backup destination is unavailable or not authorized."
	case strings.Contains(text, "artwork"):
		return "Referenced artwork changed during backup; retry the backup."
	case strings.Contains(text, "verify") || strings.Contains(text, "checksum") || strings.Contains(text, "integrity"):
		return "Backup verification failed; the previous verified backup was kept."
	default:
		return "Backup failed before verification; check storage access and retry."
	}
}

func nextRun(now time.Time, p Policy) time.Time {
	prior := time.Time{}
	if p.NextRunAt != 0 {
		prior = time.Unix(p.NextRunAt, 0)
	}
	return schedule.Next(now, p.ScheduleKind, time.Duration(p.IntervalSeconds)*time.Second, prior, p.LocalTime, p.Timezone, time.Hour)
}
func nullableInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
func (m *Manager) cleanupPartials() error {
	p, err := m.Policy()
	if err != nil || p.Destination == "" {
		return err
	}
	if _, err = os.Stat(filepath.Join(p.Destination, ownerMarker)); err != nil {
		return nil
	}
	entries, err := os.ReadDir(p.Destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".flixr-") && strings.HasSuffix(entry.Name(), ".partial") {
			info, e := entry.Info()
			if e == nil && info.Mode().IsRegular() {
				_ = os.Remove(filepath.Join(p.Destination, entry.Name()))
			}
		}
	}
	return nil
}
