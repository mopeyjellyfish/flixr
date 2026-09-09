package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	// Keep IANA schedule zones available in minimal container images.
	_ "time/tzdata"
)

const scanJobHistoryLimit = 20

type ScanPolicy struct {
	LibraryID       string   `json:"library_id"`
	Enabled         bool     `json:"enabled"`
	ScheduleKind    string   `json:"schedule_kind"`
	IntervalSeconds int64    `json:"interval_seconds"`
	LocalTime       string   `json:"local_time"`
	Timezone        string   `json:"timezone"`
	NextRunAt       int64    `json:"next_run_at,omitempty"`
	LastSuccessAt   int64    `json:"last_success_at,omitempty"`
	Exclusions      []string `json:"exclusions"`
}

type ScanJob struct {
	ID              string        `json:"id"`
	LibraryID       string        `json:"library_id"`
	Trigger         string        `json:"trigger"`
	Status          string        `json:"status"`
	QueuedAt        int64         `json:"queued_at"`
	StartedAt       int64         `json:"started_at,omitempty"`
	FinishedAt      int64         `json:"finished_at,omitempty"`
	NotBefore       int64         `json:"not_before,omitempty"`
	Attempt         int           `json:"attempt"`
	RetryOf         string        `json:"retry_of,omitempty"`
	CancelRequested bool          `json:"cancel_requested"`
	Total           *int          `json:"total,omitempty"`
	Scanned         int           `json:"scanned"`
	Skipped         int           `json:"skipped"`
	Failed          int           `json:"failed"`
	Unmatched       int           `json:"unmatched"`
	Message         string        `json:"message,omitempty"`
	Files           []ScanJobFile `json:"files,omitempty"`
}

type ScanJobFile struct {
	LocationID   string `json:"location_id"`
	RelativePath string `json:"relative_path"`
	Outcome      string `json:"outcome"`
	ErrorCode    string `json:"error_code,omitempty"`
	Message      string `json:"message,omitempty"`
	Retryable    bool   `json:"retryable"`
}

func normalizeExclusions(patterns []string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		if pattern == "" || strings.HasPrefix(pattern, "/") || pattern == ".." || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
			return nil, errors.New("exclusions must be relative paths")
		}
		pattern = strings.TrimPrefix(pattern, "./")
		if !seen[pattern] {
			seen[pattern] = true
			result = append(result, pattern)
		}
	}
	sort.Strings(result)
	return result, nil
}

func excludedScanPath(path string, patterns []string) bool {
	path = filepath.ToSlash(strings.TrimPrefix(path, "./"))
	for _, pattern := range patterns {
		runes := []rune(pattern)
		var expression strings.Builder
		expression.WriteString("^")
		for i := 0; i < len(runes); {
			if runes[i] == '*' {
				if i+1 < len(runes) && runes[i+1] == '*' {
					if i+2 < len(runes) && runes[i+2] == '/' {
						expression.WriteString("(?:.*/)?")
						i += 3
					} else {
						expression.WriteString(".*")
						i += 2
					}
				} else {
					expression.WriteString("[^/]*")
					i++
				}
				continue
			}
			expression.WriteString(regexp.QuoteMeta(string(runes[i])))
			i++
		}
		expression.WriteString("$")
		if regexp.MustCompile(expression.String()).MatchString(path) {
			return true
		}
	}
	return false
}

func (c *Catalog) scanExclusions(libraryID string) ([]string, error) {
	if c.db == nil {
		return nil, nil
	}
	rows, err := c.db.Query(`SELECT pattern FROM library_scan_exclusions WHERE library_id=? ORDER BY pattern`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	patterns := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

func (c *Catalog) ScanPolicies() ([]ScanPolicy, error) {
	if c.db == nil {
		return nil, nil
	}
	rows, err := c.db.Query(`SELECT l.id,COALESCE(p.enabled,0),COALESCE(p.schedule_kind,'interval'),COALESCE(p.interval_seconds,86400),COALESCE(p.local_time,'03:00'),COALESCE(p.timezone,'UTC'),COALESCE(p.next_run_at,0),COALESCE(p.last_success_at,0) FROM libraries l LEFT JOIN library_scan_policies p ON p.library_id=l.id ORDER BY l.created_at,l.id`)
	if err != nil {
		return nil, fmt.Errorf("load scan policies: %w", err)
	}
	defer rows.Close()
	c.schedulerMu.Lock()
	override := c.scanScheduleOverride
	c.schedulerMu.Unlock()
	var policies []ScanPolicy
	for rows.Next() {
		var p ScanPolicy
		if err := rows.Scan(&p.LibraryID, &p.Enabled, &p.ScheduleKind, &p.IntervalSeconds, &p.LocalTime, &p.Timezone, &p.NextRunAt, &p.LastSuccessAt); err != nil {
			return nil, err
		}
		p.Exclusions, err = c.scanExclusions(p.LibraryID)
		if err != nil {
			return nil, err
		}
		if override != "" {
			p, err = scanPolicyWithOverride(p, override)
			if err != nil {
				return nil, err
			}
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

func (c *Catalog) ScanPolicy(libraryID string) (ScanPolicy, error) {
	policies, err := c.ScanPolicies()
	if err != nil {
		return ScanPolicy{}, err
	}
	for _, policy := range policies {
		if policy.LibraryID == libraryID {
			return policy, nil
		}
	}
	return ScanPolicy{}, ErrLibraryNotFound
}

func (c *Catalog) ScanSchedulerRunning() bool {
	c.schedulerMu.Lock()
	defer c.schedulerMu.Unlock()
	return c.schedulerCancel != nil
}

func (c *Catalog) SetScanPolicy(libraryID string, policy ScanPolicy) (ScanPolicy, error) {
	c.scanPolicyMu.Lock()
	defer c.scanPolicyMu.Unlock()
	return c.setScanPolicy(libraryID, policy)
}

func (c *Catalog) setScanPolicy(libraryID string, policy ScanPolicy) (ScanPolicy, error) {
	if c.db == nil {
		return ScanPolicy{}, ErrLibraryNotFound
	}
	if policy.IntervalSeconds == 0 {
		policy.IntervalSeconds = 86400
	}
	if policy.LocalTime == "" {
		policy.LocalTime = "03:00"
	}
	if policy.Timezone == "" {
		policy.Timezone = "UTC"
	}
	if policy.ScheduleKind != "interval" && policy.ScheduleKind != "daily" {
		return ScanPolicy{}, errors.New("invalid schedule kind")
	}
	if policy.IntervalSeconds < 60 || policy.IntervalSeconds > 31536000 {
		return ScanPolicy{}, errors.New("invalid scan interval")
	}
	if _, err := time.Parse("15:04", policy.LocalTime); err != nil {
		return ScanPolicy{}, errors.New("invalid local scan time")
	}
	if _, err := time.LoadLocation(policy.Timezone); err != nil {
		return ScanPolicy{}, errors.New("invalid scan timezone")
	}
	exclusions, err := normalizeExclusions(policy.Exclusions)
	if err != nil {
		return ScanPolicy{}, err
	}
	policy.LibraryID, policy.Exclusions = libraryID, exclusions
	policy.NextRunAt = 0
	if policy.Enabled {
		policy.NextRunAt = nextScheduledAt(time.Now(), policy).Unix()
	}
	tx, err := c.db.Begin()
	if err != nil {
		return ScanPolicy{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO library_scan_policies(library_id,enabled,schedule_kind,interval_seconds,local_time,timezone,next_run_at,updated_at) SELECT id,?,?,?,?,?,?,? FROM libraries WHERE id=? ON CONFLICT(library_id) DO UPDATE SET enabled=excluded.enabled,schedule_kind=excluded.schedule_kind,interval_seconds=excluded.interval_seconds,local_time=excluded.local_time,timezone=excluded.timezone,next_run_at=excluded.next_run_at,updated_at=excluded.updated_at`, policy.Enabled, policy.ScheduleKind, policy.IntervalSeconds, policy.LocalTime, policy.Timezone, nullableSchedule(policy.NextRunAt), time.Now().Unix(), libraryID)
	if err != nil {
		return ScanPolicy{}, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ScanPolicy{}, ErrLibraryNotFound
	}
	if _, err := tx.Exec(`DELETE FROM library_scan_exclusions WHERE library_id=?`, libraryID); err != nil {
		return ScanPolicy{}, err
	}
	for _, pattern := range exclusions {
		if _, err := tx.Exec(`INSERT INTO library_scan_exclusions(library_id,pattern) VALUES(?,?)`, libraryID, pattern); err != nil {
			return ScanPolicy{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ScanPolicy{}, err
	}
	c.wakeScheduler()
	return c.ScanPolicy(libraryID)
}

func (c *Catalog) SetScanExclusions(libraryID string, patterns []string) (ScanPolicy, error) {
	c.scanPolicyMu.Lock()
	defer c.scanPolicyMu.Unlock()
	if c.db == nil {
		return ScanPolicy{}, ErrLibraryNotFound
	}
	exclusions, err := normalizeExclusions(patterns)
	if err != nil {
		return ScanPolicy{}, err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return ScanPolicy{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM libraries WHERE id=?`, libraryID).Scan(&exists); err != nil || exists == 0 {
		return ScanPolicy{}, ErrLibraryNotFound
	}
	if _, err := tx.Exec(`DELETE FROM library_scan_exclusions WHERE library_id=?`, libraryID); err != nil {
		return ScanPolicy{}, err
	}
	for _, pattern := range exclusions {
		if _, err := tx.Exec(`INSERT INTO library_scan_exclusions(library_id,pattern) VALUES(?,?)`, libraryID, pattern); err != nil {
			return ScanPolicy{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ScanPolicy{}, err
	}
	return c.ScanPolicy(libraryID)
}

func (c *Catalog) ApplyScanScheduleOverride(spec string) error {
	c.scanPolicyMu.Lock()
	defer c.scanPolicyMu.Unlock()
	if spec != "" {
		if _, err := scanPolicyWithOverride(ScanPolicy{ScheduleKind: "interval", IntervalSeconds: 86400, LocalTime: "03:00", Timezone: "UTC"}, spec); err != nil {
			return err
		}
	}
	c.schedulerMu.Lock()
	c.scanScheduleOverride = spec
	c.schedulerMu.Unlock()
	if c.db == nil {
		return nil
	}
	rows, err := c.db.Query(`SELECT library_id FROM library_scan_policies WHERE schedule_override<>?`, spec)
	if err != nil {
		return err
	}
	var changed []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		changed = append(changed, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range changed {
		policy, err := c.ScanPolicy(id)
		if err != nil {
			return err
		}
		next := int64(0)
		if policy.Enabled {
			policy.NextRunAt = 0
			next = nextScheduledAt(time.Now(), policy).Unix()
		}
		if _, err := c.db.Exec(`UPDATE library_scan_policies SET schedule_override=?,next_run_at=?,updated_at=? WHERE library_id=?`, spec, nullableSchedule(next), time.Now().Unix(), id); err != nil {
			return err
		}
	}
	c.wakeScheduler()
	return nil
}

func scanPolicyWithOverride(policy ScanPolicy, spec string) (ScanPolicy, error) {
	switch {
	case spec == "off":
		policy.Enabled = false
	case strings.HasPrefix(spec, "every:"):
		duration, err := time.ParseDuration(strings.TrimPrefix(spec, "every:"))
		if err != nil || duration < time.Minute || duration > 365*24*time.Hour {
			return policy, errors.New("invalid scan schedule override")
		}
		policy.Enabled = true
		policy.ScheduleKind = "interval"
		policy.IntervalSeconds = int64(duration / time.Second)
	case strings.HasPrefix(spec, "daily:"):
		parts := strings.SplitN(strings.TrimPrefix(spec, "daily:"), "@", 2)
		if len(parts) != 2 {
			return policy, errors.New("invalid scan schedule override")
		}
		if _, err := time.Parse("15:04", parts[0]); err != nil {
			return policy, err
		}
		if _, err := time.LoadLocation(parts[1]); err != nil {
			return policy, err
		}
		policy.Enabled = true
		policy.ScheduleKind = "daily"
		policy.LocalTime = parts[0]
		policy.Timezone = parts[1]
	default:
		return policy, errors.New("invalid scan schedule override")
	}
	return policy, nil
}

func nullableSchedule(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nextScheduledAt(now time.Time, policy ScanPolicy) time.Time {
	if policy.ScheduleKind == "interval" {
		d := time.Duration(policy.IntervalSeconds) * time.Second
		if d < time.Minute {
			d = time.Minute
		}
		next := time.Unix(policy.NextRunAt, 0)
		if policy.NextRunAt == 0 {
			return now.Add(d)
		}
		for !next.After(now) {
			missed := now.Sub(next)/d + 1
			next = next.Add(missed * d)
		}
		return next
	}
	location, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		location = time.UTC
	}
	parsed, err := time.Parse("15:04", policy.LocalTime)
	if err != nil {
		parsed = time.Date(0, 1, 1, 3, 0, 0, 0, time.UTC)
	}
	localNow := now.In(location)
	for dayOffset := 0; dayOffset < 3; dayOffset++ {
		date := localNow.AddDate(0, 0, dayOffset)
		var exact time.Time
		var fallback time.Time
		start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location).Add(-3 * time.Hour)
		for candidate := start; candidate.Before(start.Add(32 * time.Hour)); candidate = candidate.Add(time.Minute) {
			wall := candidate.In(location)
			if wall.Year() != date.Year() || wall.YearDay() != date.YearDay() {
				continue
			}
			minutes := wall.Hour()*60 + wall.Minute()
			target := parsed.Hour()*60 + parsed.Minute()
			if minutes >= target && fallback.IsZero() {
				fallback = candidate
			}
			if minutes == target && exact.IsZero() {
				exact = candidate
			}
		}
		scheduled := exact
		if scheduled.IsZero() {
			scheduled = fallback
		}
		if !scheduled.IsZero() && scheduled.After(now) {
			return scheduled
		}
	}
	return now.Add(24 * time.Hour)
}

func (c *Catalog) QueueLibraryScan(libraryID, trigger string) (ScanJob, error) {
	if trigger != "manual" && trigger != "schedule" && trigger != "startup" && trigger != "retry" {
		return ScanJob{}, errors.New("invalid scan trigger")
	}
	if c.db == nil {
		return ScanJob{}, ErrLibraryNotFound
	}
	existing, err := scanJobRow(c.db.QueryRow(`SELECT id,library_id,trigger,status,queued_at,COALESCE(started_at,0),COALESCE(finished_at,0),not_before,attempt,retry_of,cancel_requested,total,scanned,skipped,failed,unmatched,message FROM scan_jobs WHERE library_id=? AND status IN ('queued','running') ORDER BY queued_at LIMIT 1`, libraryID))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ScanJob{}, err
	}
	var exists int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM libraries WHERE id=?`, libraryID).Scan(&exists); err != nil || exists == 0 {
		return ScanJob{}, ErrLibraryNotFound
	}
	id, err := randomScanID()
	if err != nil {
		return ScanJob{}, err
	}
	now := time.Now().Unix()
	if _, err := c.db.Exec(`INSERT INTO scan_jobs(id,library_id,trigger,status,queued_at,not_before) VALUES(?,?,?,'queued',?,?)`, id, libraryID, trigger, now, now); err != nil {
		existing, lookupErr := scanJobRow(c.db.QueryRow(`SELECT id,library_id,trigger,status,queued_at,COALESCE(started_at,0),COALESCE(finished_at,0),not_before,attempt,retry_of,cancel_requested,total,scanned,skipped,failed,unmatched,message FROM scan_jobs WHERE library_id=? AND status IN ('queued','running') ORDER BY queued_at LIMIT 1`, libraryID))
		if lookupErr == nil {
			return existing, nil
		}
		return ScanJob{}, err
	}
	job, err := c.scanJob(id)
	if err == nil {
		c.wakeScheduler()
	}
	return job, err
}

type rowScanner interface{ Scan(...any) error }

func scanJobRow(row rowScanner) (ScanJob, error) {
	var job ScanJob
	var cancel int
	var total sql.NullInt64
	err := row.Scan(&job.ID, &job.LibraryID, &job.Trigger, &job.Status, &job.QueuedAt, &job.StartedAt, &job.FinishedAt, &job.NotBefore, &job.Attempt, &job.RetryOf, &cancel, &total, &job.Scanned, &job.Skipped, &job.Failed, &job.Unmatched, &job.Message)
	job.CancelRequested = cancel != 0
	if total.Valid {
		n := int(total.Int64)
		job.Total = &n
	}
	return job, err
}

func (c *Catalog) scanJob(id string) (ScanJob, error) {
	job, err := scanJobRow(c.db.QueryRow(`SELECT id,library_id,trigger,status,queued_at,COALESCE(started_at,0),COALESCE(finished_at,0),not_before,attempt,retry_of,cancel_requested,total,scanned,skipped,failed,unmatched,message FROM scan_jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ScanJob{}, errors.New("scan job not found")
	}
	if err != nil {
		return ScanJob{}, err
	}
	rows, err := c.db.Query(`SELECT location_id,relative_path,outcome,error_code,message,retryable FROM scan_job_files WHERE job_id=? ORDER BY location_id,relative_path`, id)
	if err != nil {
		return ScanJob{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var file ScanJobFile
		if err := rows.Scan(&file.LocationID, &file.RelativePath, &file.Outcome, &file.ErrorCode, &file.Message, &file.Retryable); err != nil {
			return ScanJob{}, err
		}
		job.Files = append(job.Files, file)
	}
	return job, rows.Err()
}

func (c *Catalog) ScanJob(id string) (ScanJob, error) { return c.scanJob(id) }

func (c *Catalog) ScanJobs(limit int) ([]ScanJob, error) {
	if c.db == nil {
		return nil, nil
	}
	if limit < 1 || limit > 100 {
		limit = scanJobHistoryLimit
	}
	rows, err := c.db.Query(`SELECT id FROM scan_jobs ORDER BY queued_at DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	jobs := make([]ScanJob, 0, len(ids))
	for _, id := range ids {
		job, err := c.scanJob(id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (c *Catalog) QueueAllLibraryScans(trigger string) ([]ScanJob, error) {
	libraries, err := c.Libraries()
	if err != nil {
		return nil, err
	}
	jobs := make([]ScanJob, 0, len(libraries))
	for _, library := range libraries {
		job, err := c.QueueLibraryScan(library.ID, trigger)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (c *Catalog) CancelScanJob(id string) error {
	if c.db == nil {
		return errors.New("scan job not found")
	}
	c.scanCommitMu.Lock()
	c.schedulerMu.Lock()
	active := c.activeJobID == id && c.activeJobCancel != nil && !c.activeJobCommitted
	if active {
		c.activeJobOwnerCancelled = true
		c.activeJobCancel()
	}
	c.schedulerMu.Unlock()
	c.scanCommitMu.Unlock()
	if active {
		if _, err := c.db.Exec(`UPDATE scan_jobs SET cancel_requested=1 WHERE id=?`, id); err != nil {
			return err
		}
		c.wakeScheduler()
		return nil
	}
	result, err := c.db.Exec(`UPDATE scan_jobs SET cancel_requested=1,status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END,finished_at=CASE WHEN status='queued' THEN ? ELSE finished_at END,message=CASE WHEN status='queued' THEN 'Cancelled before starting.' ELSE message END WHERE id=? AND status IN ('queued','running')`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return errors.New("scan job not active")
	}
	c.wakeScheduler()
	if job, lookupErr := c.scanJob(id); lookupErr == nil && job.Status == "cancelled" {
		_ = c.pruneScanJobHistory(job.LibraryID)
	}
	return nil
}

func (c *Catalog) RetryScanJob(id string, selected []ScanJobFile) (ScanJob, error) {
	prior, err := c.scanJob(id)
	if err != nil {
		return ScanJob{}, err
	}
	if prior.Status != "partial" && prior.Status != "failed" && prior.Status != "interrupted" && prior.Status != "cancelled" {
		return ScanJob{}, errors.New("scan job is not retryable")
	}
	if prior.Attempt >= 3 {
		return ScanJob{}, errors.New("scan retry limit reached")
	}
	available := make(map[string]ScanJobFile, len(prior.Files))
	for _, file := range prior.Files {
		available[file.LocationID+"\x00"+file.RelativePath] = file
	}
	if len(selected) == 0 {
		for _, file := range prior.Files {
			if file.Retryable {
				selected = append(selected, file)
			}
		}
	} else {
		validated := make([]ScanJobFile, 0, len(selected))
		seen := map[string]bool{}
		for _, requested := range selected {
			key := requested.LocationID + "\x00" + requested.RelativePath
			file, ok := available[key]
			if !ok || !file.Retryable {
				return ScanJob{}, errors.New("selected file is not retryable")
			}
			if !seen[key] {
				validated = append(validated, file)
				seen[key] = true
			}
		}
		selected = validated
	}
	wholeLibrary := len(selected) == 0 && (prior.Status == "failed" || prior.Status == "interrupted" || prior.Status == "cancelled")
	if len(selected) == 0 && !wholeLibrary {
		return ScanJob{}, errors.New("scan job has no retryable files")
	}
	newID, err := randomScanID()
	if err != nil {
		return ScanJob{}, err
	}
	now := time.Now().Unix()
	tx, err := c.db.Begin()
	if err != nil {
		return ScanJob{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO scan_jobs(id,library_id,trigger,status,queued_at,not_before,attempt,retry_of) VALUES(?,?,'retry','queued',?,?,?,?)`, newID, prior.LibraryID, now, now, prior.Attempt+1, prior.ID); err != nil {
		return ScanJob{}, err
	}
	for _, file := range selected {
		if _, err = tx.Exec(`INSERT INTO scan_job_files(job_id,location_id,relative_path,outcome,error_code,message,retryable) VALUES(?,?,?,'queued','','',1)`, newID, file.LocationID, file.RelativePath); err != nil {
			return ScanJob{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return ScanJob{}, err
	}
	c.wakeScheduler()
	return c.scanJob(newID)
}

func (c *Catalog) wakeScheduler() {
	c.schedulerMu.Lock()
	wake := c.schedulerWake
	c.schedulerMu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (c *Catalog) StartScanScheduler(ctx context.Context, workers int) error {
	if c.db == nil {
		return nil
	}
	if workers < 1 {
		workers = 1
	}
	c.schedulerMu.Lock()
	defer c.schedulerMu.Unlock()
	if c.schedulerCancel != nil {
		return errors.New("scan scheduler already running")
	}
	if _, err := c.db.Exec(`UPDATE scan_jobs SET status='interrupted',finished_at=?,message='Interrupted by server restart.' WHERE status='running'`, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := c.db.Exec(`UPDATE scan_runs SET status='interrupted',finished_at=?,message='Interrupted by server restart.' WHERE status='running'`, time.Now().Unix()); err != nil {
		return err
	}
	schedulerCtx, cancel := context.WithCancel(ctx)
	c.schedulerCancel = cancel
	c.schedulerDone = make(chan struct{})
	c.schedulerWake = make(chan struct{}, 1)
	c.schedulerWorkers = workers
	go c.runScanScheduler(schedulerCtx)
	return nil
}

func (c *Catalog) runScanScheduler(ctx context.Context) {
	defer func() {
		c.schedulerMu.Lock()
		close(c.schedulerDone)
		c.schedulerDone = nil
		c.schedulerCancel = nil
		c.schedulerWake = nil
		c.schedulerMu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		_ = c.enqueueDueScans(time.Now())
		job, err := c.claimScanJob(time.Now())
		if err == nil {
			c.executeScanJob(ctx, job)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		c.schedulerMu.Lock()
		wake := c.schedulerWake
		c.schedulerMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-time.After(time.Second):
		}
	}
}

func (c *Catalog) enqueueDueScans(now time.Time) error {
	c.scanPolicyMu.Lock()
	defer c.scanPolicyMu.Unlock()
	policies, err := c.ScanPolicies()
	if err != nil {
		return err
	}
	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		if policy.NextRunAt == 0 || policy.NextRunAt <= now.Unix() {
			if _, err := c.QueueLibraryScan(policy.LibraryID, "schedule"); err != nil {
				return err
			}
			policy.NextRunAt = nextScheduledAt(now, policy).Unix()
			if _, err := c.db.Exec(`UPDATE library_scan_policies SET next_run_at=?,updated_at=? WHERE library_id=?`, policy.NextRunAt, now.Unix(), policy.LibraryID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Catalog) claimScanJob(now time.Time) (ScanJob, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return ScanJob{}, err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRow(`SELECT id FROM scan_jobs WHERE status='queued' AND cancel_requested=0 AND not_before<=? ORDER BY queued_at,id LIMIT 1`, now.Unix()).Scan(&id); err != nil {
		return ScanJob{}, err
	}
	result, err := tx.Exec(`UPDATE scan_jobs SET status='running',started_at=? WHERE id=? AND status='queued'`, now.Unix(), id)
	if err != nil {
		return ScanJob{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ScanJob{}, sql.ErrNoRows
	}
	if err = tx.Commit(); err != nil {
		return ScanJob{}, err
	}
	return c.scanJob(id)
}

func (c *Catalog) executeScanJob(parent context.Context, job ScanJob) {
	ctx, cancel := context.WithCancel(parent)
	c.schedulerMu.Lock()
	c.activeJobID = job.ID
	c.activeJobCancel = cancel
	c.activeJobCommitted = false
	c.activeJobOwnerCancelled = false
	c.schedulerMu.Unlock()
	var cancelRequested int
	_ = c.db.QueryRow(`SELECT cancel_requested FROM scan_jobs WHERE id=?`, job.ID).Scan(&cancelRequested)
	if cancelRequested != 0 {
		c.schedulerMu.Lock()
		if c.activeJobID == job.ID {
			c.activeJobOwnerCancelled = true
		}
		c.schedulerMu.Unlock()
		cancel()
	}
	retry := map[string]bool{}
	if job.Trigger == "retry" {
		for _, file := range job.Files {
			retry[file.LocationID+"\x00"+file.RelativePath] = true
		}
	}
	err := c.scanLibrary(ctx, c.schedulerWorkers, job.LibraryID, retry)
	c.schedulerMu.Lock()
	ownerCancelled := c.activeJobOwnerCancelled
	c.activeJobID = ""
	c.activeJobCancel = nil
	c.activeJobCommitted = false
	c.activeJobOwnerCancelled = false
	c.schedulerMu.Unlock()
	cancel()
	if errors.Is(err, ErrScanActive) {
		_, _ = c.db.Exec(`UPDATE scan_jobs SET status='queued',started_at=NULL,not_before=? WHERE id=?`, time.Now().Add(time.Second).Unix(), job.ID)
		return
	}
	status := c.ScanStatus()
	terminal := "succeeded"
	message := ""
	if errors.Is(err, context.Canceled) {
		if ownerCancelled {
			terminal = "cancelled"
			message = "Cancelled by owner."
		} else {
			terminal = "interrupted"
			message = "Interrupted by server shutdown."
		}
	} else if err != nil || status.Status == "failed" {
		terminal = "failed"
		message = redactScanError(status.Message)
	} else if status.Status == "partial" || status.Status == "review_required" {
		terminal = "partial"
		message = redactScanError(status.Message)
	}
	finishedAt := time.Now().Unix()
	if finishErr := c.finishScanJob(job, status, terminal, message, finishedAt); finishErr != nil {
		_, _ = c.db.Exec(`UPDATE scan_jobs SET status='failed',finished_at=?,message='The scan finished, but its result could not be recorded. Run it again.' WHERE id=? AND status='running'`, finishedAt, job.ID)
		return
	}
	rawLower := strings.ToLower(status.Message)
	permanent := strings.Contains(rawLower, "permission") || strings.Contains(rawLower, "unavailable") || strings.Contains(rawLower, "not exist")
	if (terminal == "partial" || terminal == "failed") && job.Attempt < 3 && !permanent {
		if retryJob, retryErr := c.RetryScanJob(job.ID, nil); retryErr == nil {
			delay := time.Minute
			if job.Attempt > 1 {
				delay = 5 * time.Minute
			}
			_, _ = c.db.Exec(`UPDATE scan_jobs SET not_before=? WHERE id=?`, time.Now().Add(delay).Unix(), retryJob.ID)
		}
	}
	_ = c.pruneScanJobHistory(job.LibraryID)
}

func (c *Catalog) finishScanJob(job ScanJob, status ScanStatus, terminal, message string, finishedAt int64) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE scan_jobs SET status=?,finished_at=?,total=?,scanned=?,skipped=?,failed=?,unmatched=?,message=? WHERE id=? AND status='running'`, terminal, finishedAt, nullableInt(status.Total), status.Scanned, status.Skipped, status.Failed, status.Unmatched, message, job.ID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("scan job is no longer running")
	}
	if err := c.captureScanJobFilesTx(tx, job.ID, job.LibraryID, status.ID); err != nil {
		return err
	}
	if terminal == "succeeded" {
		if _, err := tx.Exec(`UPDATE library_scan_policies SET last_success_at=?,updated_at=? WHERE library_id=?`, finishedAt, time.Now().UnixNano(), job.LibraryID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Catalog) pruneScanJobHistory(libraryID string) error {
	_, err := c.db.Exec(`DELETE FROM scan_jobs WHERE id IN (SELECT id FROM scan_jobs WHERE library_id=? AND status NOT IN ('queued','running') ORDER BY queued_at DESC,id DESC LIMIT -1 OFFSET ?)`, libraryID, scanJobHistoryLimit)
	return err
}

func redactScanError(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "permission"):
		return "A library file or folder could not be read. Check its permissions and retry."
	case strings.Contains(lower, "not exist") || strings.Contains(lower, "unavailable"):
		return "A library folder is unavailable. Reconnect it and retry."
	case message != "":
		return "The scan could not finish. Review the affected files and retry."
	default:
		return ""
	}
}

func (c *Catalog) captureScanJobFilesTx(tx *sql.Tx, jobID, libraryID, scanID string) error {
	if _, err := tx.Exec(`UPDATE scan_job_files SET outcome='succeeded',error_code='',message='',retryable=0 WHERE job_id=? AND outcome='queued'`, jobID); err != nil {
		return err
	}
	rows, err := c.db.Query(`SELECT relative_path,outcome,message FROM scan_files WHERE scan_id=? AND outcome IN ('failed','unmatched') ORDER BY relative_path`, scanID)
	if err != nil {
		return err
	}
	defer rows.Close()
	libraries, err := c.Libraries()
	if err != nil {
		return err
	}
	locationID := ""
	for _, library := range libraries {
		if library.ID == libraryID && len(library.Locations) == 1 {
			locationID = library.Locations[0].ID
		}
	}
	for rows.Next() {
		var identifier, outcome, raw string
		if err := rows.Scan(&identifier, &outcome, &raw); err != nil {
			return err
		}
		relative := identifier
		if index := strings.Index(relative, "\t"); index >= 0 {
			locationID, relative = relative[:index], relative[index+1:]
		}
		if index := strings.Index(relative, ":"); index >= 0 {
			relative = relative[index+1:]
		}
		code := "probe_failed"
		retryable := outcome == "failed"
		if outcome == "unmatched" {
			code = "metadata_unmatched"
			retryable = false
		}
		message := "Metadata could not match this file."
		if retryable {
			message = "This file could not be inspected. Retry after checking that it is readable."
			switch {
			case strings.HasPrefix(raw, "excluded_by_policy:"):
				code = "excluded_by_policy"
				retryable = false
				message = strings.TrimSpace(strings.TrimPrefix(raw, "excluded_by_policy:"))
			case strings.HasPrefix(raw, "not_found:"):
				code = "not_found"
				retryable = false
				message = strings.TrimSpace(strings.TrimPrefix(raw, "not_found:"))
			case strings.Contains(strings.ToLower(raw), "permission"):
				code = "permission_denied"
				retryable = false
				message = "This file could not be read. Check its permissions before the next scan."
			case strings.Contains(strings.ToLower(raw), "ffprobe") && (strings.Contains(strings.ToLower(raw), "not found") || strings.Contains(strings.ToLower(raw), "unavailable")):
				code = "ffprobe_unavailable"
				retryable = false
				message = "FFprobe is unavailable. Install or configure it before the next scan."
			}
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO scan_job_files(job_id,location_id,relative_path,outcome,error_code,message,retryable) VALUES(?,?,?,?,?,?,?)`, jobID, locationID, relative, outcome, code, message, retryable); err != nil {
			return err
		}
	}
	return rows.Err()
}
