package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestExcludedScanPathSupportsTreeAndSegmentPatterns(t *testing.T) {
	tests := map[string]bool{
		"Extras/trailer.mp4":       true,
		"Season 1/Samples/a.mkv":   true,
		"Samples/a.mkv":            true,
		"Season 1/episode 1.mkv":   false,
		"private/film.mp4":         true,
		"private-library/film.mp4": false,
	}
	patterns := []string{"Extras/**", "**/Samples/**", "private/**"}
	for path, want := range tests {
		if got := excludedScanPath(path, patterns); got != want {
			t.Errorf("excludedScanPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSchedulerPollsLibraryAndSkipsUnchangedMedia(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var probes atomic.Int32
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) { probes.Add(1); return MediaProperties{}, nil }))
	if err != nil || c.SetRoots(root, "") != nil {
		t.Fatalf("open: %v", err)
	}
	if err := c.StartScanScheduler(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	first, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	first = waitScanJob(t, c, first.ID)
	if first.Status != "succeeded" || first.Scanned != 1 || first.Skipped != 0 {
		t.Fatalf("first job = %#v", first)
	}
	second, err := c.QueueLibraryScan("films", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	second = waitScanJob(t, c, second.ID)
	if second.Status != "succeeded" || second.Scanned != 0 || second.Skipped != 1 || probes.Load() != 1 {
		t.Fatalf("incremental job = %#v, probes=%d", second, probes.Load())
	}
	if err := os.WriteFile(filepath.Join(root, "new.mp4"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	third, err := c.QueueLibraryScan("films", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	third = waitScanJob(t, c, third.ID)
	if third.Status != "succeeded" || third.Scanned != 1 || third.Skipped != 1 || probes.Load() != 2 {
		t.Fatalf("polling job = %#v, probes=%d", third, probes.Load())
	}
}

func TestSchedulerMarksRunningJobInterruptedOnRestart(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE scan_jobs SET status='running',started_at=? WHERE id=?`, time.Now().Unix(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.StartScanScheduler(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	job, err = c.ScanJob(job.ID)
	if err != nil || job.Status != "interrupted" {
		t.Fatalf("restarted job = %#v, %v", job, err)
	}
}

func TestLibraryExclusionsAvoidProbeAndPreserveKnownTitles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Extras"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"film.mp4", "Extras/trailer.mp4"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var probes atomic.Int32
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) { probes.Add(1); return MediaProperties{}, nil }))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	if _, err := c.SetScanPolicy("films", ScanPolicy{ScheduleKind: "interval", IntervalSeconds: 3600, Exclusions: []string{"Extras/**"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Extras", "trailer.mp4"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Extras", "new.mp4"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.scanLibrary(t.Context(), 1, "films", nil); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 || probes.Load() != 2 {
		t.Fatalf("excluded scan items=%d probes=%d err=%v", len(items), probes.Load(), err)
	}
}

func TestScanJobCancelAndPerFileRetry(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "film.mp4"), []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
		db, err := sqlite.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		entered := make(chan struct{}, 1)
		c, err := OpenWithProber(db, ProberFunc(func(ctx context.Context, _ *os.File) (MediaProperties, error) {
			entered <- struct{}{}
			<-ctx.Done()
			return MediaProperties{}, ctx.Err()
		}))
		if err != nil || c.SetRoots(root, "") != nil {
			t.Fatal(err)
		}
		if err := c.StartScanScheduler(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		defer c.Shutdown(context.Background())
		job, err := c.QueueLibraryScan("films", "manual")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("scan did not enter prober")
		}
		if err := c.CancelScanJob(job.ID); err != nil {
			t.Fatal(err)
		}
		job = waitScanJob(t, c, job.ID)
		if job.Status != "cancelled" {
			t.Fatalf("cancelled job = %#v", job)
		}
	})
	t.Run("retry only failed file", func(t *testing.T) {
		root := t.TempDir()
		for _, name := range []string{"good.mp4", "bad.mp4"} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
		db, err := sqlite.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		fixed := false
		calls := map[string]int{}
		c, err := OpenWithProber(db, ProberFunc(func(_ context.Context, file *os.File) (MediaProperties, error) {
			name := filepath.Base(file.Name())
			calls[name]++
			if name == "bad.mp4" && !fixed {
				return MediaProperties{}, errors.New("temporary probe failure")
			}
			return MediaProperties{}, nil
		}))
		if err != nil || c.SetRoots(root, "") != nil {
			t.Fatal(err)
		}
		if err := c.StartScanScheduler(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		defer c.Shutdown(context.Background())
		first, err := c.QueueLibraryScan("films", "manual")
		if err != nil {
			t.Fatal(err)
		}
		first = waitScanJob(t, c, first.ID)
		if first.Status != "partial" || len(first.Files) != 1 || !first.Files[0].Retryable {
			t.Fatalf("failed job = %#v", first)
		}
		if _, err := c.RetryScanJob(first.ID, []ScanJobFile{{LocationID: "films", RelativePath: "good.mp4", Retryable: true}}); err == nil {
			t.Fatal("retry accepted a file that was not retryable in the failed job")
		}
		// Remove the scheduler-created delayed retry so this test exercises the explicit action.
		if _, err := db.Exec(`DELETE FROM scan_jobs WHERE retry_of=?`, first.ID); err != nil {
			t.Fatal(err)
		}
		fixed = true
		retry, err := c.RetryScanJob(first.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		retry = waitScanJob(t, c, retry.ID)
		if retry.Status != "succeeded" || retry.Scanned != 1 || calls["good.mp4"] != 1 || calls["bad.mp4"] != 2 {
			t.Fatalf("retry=%#v calls=%#v", retry, calls)
		}
	})
}

func waitScanJob(t *testing.T, c *Catalog, id string) ScanJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := c.ScanJob(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "queued" && job.Status != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scan job did not finish")
	return ScanJob{}
}

func TestScanPolicyQueueCoalescesAndSurvivesRestart(t *testing.T) {
	data := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := c.SetScanPolicy("films", ScanPolicy{Enabled: true, ScheduleKind: "interval", IntervalSeconds: 300, Timezone: "UTC", Exclusions: []string{"Extras/**"}})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Enabled || policy.NextRunAt == 0 || len(policy.Exclusions) != 1 {
		t.Fatalf("saved policy = %#v", policy)
	}
	first, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.QueueLibraryScan("films", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Trigger != "manual" {
		t.Fatalf("coalesced jobs = %#v %#v", first, second)
	}
	db.Close()

	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := c.ScanPolicies()
	if err != nil || len(policies) < 1 || policies[0].LibraryID == "" {
		t.Fatalf("reloaded policies = %#v, %v", policies, err)
	}
	jobs, err := c.ScanJobs(10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != first.ID {
		t.Fatalf("reloaded jobs = %#v, %v", jobs, err)
	}
}

func TestScheduleOverrideAppliesToLibraryCreatedAfterStartup(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyScanScheduleOverride("every:1h"); err != nil {
		t.Fatal(err)
	}
	library, err := c.CreateLibrary("Documentaries", "film")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := c.ScanPolicy(library.ID)
	if err != nil || !policy.Enabled || policy.Exclusions == nil {
		t.Fatalf("new library policy = %#v, %v", policy, err)
	}
	now := time.Now()
	if err := c.enqueueDueScans(now); err != nil {
		t.Fatal(err)
	}
	policy, err = c.ScanPolicy(library.ID)
	if err != nil || policy.NextRunAt <= now.Unix() {
		t.Fatalf("persisted next run = %#v, %v", policy, err)
	}
	jobs, err := c.ScanJobs(10)
	if err != nil || len(jobs) != 1 || jobs[0].LibraryID != library.ID {
		t.Fatalf("new library jobs = %#v, %v", jobs, err)
	}
}

func TestConcurrentScanTriggersCoalescePerLibrary(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 24)
	errs := make(chan error, 24)
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := c.QueueLibraryScan("films", "manual")
			if err != nil {
				errs <- err
				return
			}
			ids <- job.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Errorf("queue: %v", err)
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatalf("active job IDs = %#v", unique)
	}
}

func TestCancelledJobHistoryRemainsBounded(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for range scanJobHistoryLimit + 5 {
		job, err := c.QueueLibraryScan("films", "manual")
		if err != nil {
			t.Fatal(err)
		}
		if err := c.CancelScanJob(job.ID); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := c.ScanJobs(100)
	if err != nil || len(jobs) != scanJobHistoryLimit {
		t.Fatalf("cancelled history length = %d, want %d: %v", len(jobs), scanJobHistoryLimit, err)
	}
}

func TestNextScheduledAtCoalescesMissedIntervalsAndHandlesDST(t *testing.T) {
	now := time.Date(2026, 3, 29, 2, 30, 0, 0, time.UTC)
	interval := ScanPolicy{ScheduleKind: "interval", IntervalSeconds: 3600, NextRunAt: now.Add(-5 * time.Hour).Unix()}
	if got := nextScheduledAt(now, interval); !got.After(now) || got.After(now.Add(time.Hour)) {
		t.Fatalf("coalesced interval next = %v", got)
	}
	daily := ScanPolicy{ScheduleKind: "daily", LocalTime: "01:30", Timezone: "Europe/London"}
	got := nextScheduledAt(time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), daily)
	local := got.In(mustLocation(t, "Europe/London"))
	if local.Day() != 29 || local.Hour() != 2 || local.Minute() != 0 {
		t.Fatalf("spring-forward daily next = %v (%v local), want first valid instant after 01:30", got, local)
	}
	fallBack := ScanPolicy{ScheduleKind: "daily", LocalTime: "01:30", Timezone: "Europe/London"}
	first := nextScheduledAt(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), fallBack)
	if want := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC); !first.Equal(want) {
		t.Fatalf("fall-back first occurrence = %v, want %v", first, want)
	}
	afterFirst := nextScheduledAt(time.Date(2026, 10, 25, 0, 45, 0, 0, time.UTC), fallBack)
	if want := time.Date(2026, 10, 26, 1, 30, 0, 0, time.UTC); !afterFirst.Equal(want) {
		t.Fatalf("fall-back next day = %v, want %v", afterFirst, want)
	}
}

func TestScheduleEnvironmentOverrideDoesNotReplaceOwnerPolicy(t *testing.T) {
	data := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetScanPolicy("films", ScanPolicy{Enabled: true, ScheduleKind: "interval", IntervalSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyScanScheduleOverride("off"); err != nil {
		t.Fatal(err)
	}
	effective, err := c.ScanPolicy("films")
	if err != nil || effective.Enabled {
		t.Fatalf("effective override = %#v, %v", effective, err)
	}
	_ = c.Shutdown(context.Background())
	db.Close()
	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyScanScheduleOverride(""); err != nil {
		t.Fatal(err)
	}
	saved, err := c.ScanPolicy("films")
	if err != nil || !saved.Enabled || saved.IntervalSeconds != 300 {
		t.Fatalf("saved policy = %#v, %v", saved, err)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
