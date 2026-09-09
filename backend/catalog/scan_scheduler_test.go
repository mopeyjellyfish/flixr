package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	policy, err := c.ScanPolicy("films")
	if err != nil || policy.LastSuccessAt == 0 || policy.LastSuccessAt != first.FinishedAt {
		t.Fatalf("last successful completion = %#v, job=%#v, err=%v", policy, first, err)
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

func TestIncrementalScanDoesNotReprobeUnchangedMediaWithSidecars(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"film.mp4", "film.eng.aac"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := map[string]int{}
	c, err := OpenWithProber(db, ProberFunc(func(_ context.Context, file *os.File) (MediaProperties, error) {
		calls[filepath.Base(file.Name())]++
		if filepath.Ext(file.Name()) == ".aac" {
			return MediaProperties{Audio: []AudioTrack{{Index: 0, Codec: "aac"}}}, nil
		}
		return MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil {
		t.Fatalf("open: %v", err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	status := c.ScanStatus()
	if calls["film.mp4"] != 1 || calls["film.eng.aac"] != 2 || status.Scanned != 0 || status.Skipped != 1 {
		t.Fatalf("calls=%#v status=%#v", calls, status)
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

func TestCancellingWhilePersistenceWaitsDoesNotPublishScan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "film.mp4")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var probes atomic.Int32
	c, err := OpenWithProber(db, ProberFunc(func(_ context.Context, _ *os.File) (MediaProperties, error) {
		if probes.Add(1) > 1 {
			entered <- struct{}{}
			<-release
		}
		return MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	before, err := c.List("", 0, 10)
	if err != nil || len(before) != 1 {
		t.Fatalf("initial catalog = %#v, %v", before, err)
	}
	if err := os.WriteFile(path, []byte("changed media"), 0600); err != nil {
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
		t.Fatal("changed file did not enter probe")
	}
	writerBlock, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- c.CancelScanJob(job.ID) }()
	time.Sleep(50 * time.Millisecond)
	if err := writerBlock.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not finish after the writer became available")
	}
	job = waitScanJob(t, c, job.ID)
	after, err := c.List("", 0, 10)
	if err != nil || job.Status != "cancelled" || len(after) != 1 || after[0].ID != before[0].ID || !after[0].Playable {
		t.Fatalf("cancelled job=%#v catalog=%#v err=%v", job, after, err)
	}
}

func TestCancelAfterClaimBeforeExecutionRegistration(t *testing.T) {
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
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		probes.Add(1)
		return MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil {
		t.Fatal(err)
	}
	queued, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := c.claimScanJob(time.Now())
	if err != nil || claimed.ID != queued.ID {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	if err := c.CancelScanJob(claimed.ID); err != nil {
		t.Fatalf("cancel in claim window: %v", err)
	}
	c.executeScanJob(t.Context(), claimed)
	job, err := c.ScanJob(claimed.ID)
	if err != nil || job.Status != "cancelled" || probes.Load() != 0 {
		t.Fatalf("claim-window cancellation job=%#v probes=%d err=%v", job, probes.Load(), err)
	}
}

func TestCancelIntentCrossingExecutionRegistrationIsConsumed(t *testing.T) {
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
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		probes.Add(1)
		return MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil {
		t.Fatal(err)
	}
	queued, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := c.claimScanJob(time.Now())
	if err != nil || claimed.ID != queued.ID {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	markerReached := make(chan struct{})
	releaseMarker := make(chan struct{})
	c.cancelMarkerHook = func() {
		close(markerReached)
		<-releaseMarker
	}
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- c.CancelScanJob(claimed.ID) }()
	<-markerReached
	executeDone := make(chan struct{})
	go func() {
		c.executeScanJob(t.Context(), claimed)
		close(executeDone)
	}()
	select {
	case <-executeDone:
	case <-time.After(time.Second):
		t.Fatal("execution did not consume the pending cancellation intent")
	}
	if probes.Load() != 0 {
		t.Fatalf("crossed cancellation probed %d files", probes.Load())
	}
	close(releaseMarker)
	if err := <-cancelDone; err != nil {
		t.Fatalf("consumed cancellation returned an error: %v", err)
	}
	c.cancelMarkerHook = nil
	job, err := c.ScanJob(claimed.ID)
	if err != nil || job.Status != "cancelled" {
		t.Fatalf("crossed cancellation job=%#v err=%v", job, err)
	}
	if err := c.CancelScanJob("missing-job"); err == nil {
		t.Fatal("missing job cancellation succeeded")
	}
	failing, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_cancel_marker BEFORE UPDATE OF cancel_requested ON scan_jobs WHEN OLD.id=%q BEGIN SELECT RAISE(FAIL,'injected cancellation failure'); END`, failing.ID)); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelScanJob(failing.ID); err == nil {
		t.Fatal("injected cancellation marker failure was ignored")
	}
	c.schedulerMu.Lock()
	pending := len(c.pendingJobCancellations)
	c.schedulerMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending cancellation intents retained after terminal paths: %d", pending)
	}
}

func TestCancelIsRejectedAfterCatalogCommitUntilJobIsTerminal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		return MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil {
		t.Fatal(err)
	}
	queued, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := c.claimScanJob(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	terminalReached := make(chan struct{})
	releaseTerminal := make(chan struct{})
	c.jobTerminalHook = func() {
		close(terminalReached)
		<-releaseTerminal
	}
	executeDone := make(chan struct{})
	go func() {
		c.executeScanJob(t.Context(), claimed)
		close(executeDone)
	}()
	<-terminalReached
	if err := c.CancelScanJob(queued.ID); err == nil {
		t.Fatal("cancellation was acknowledged after the catalog commit")
	}
	close(releaseTerminal)
	<-executeDone
	job, err := c.ScanJob(queued.ID)
	if err != nil || job.Status != "succeeded" {
		t.Fatalf("sealed terminal job=%#v err=%v", job, err)
	}
}

func TestTerminalWriteFailureReleasesLibraryQueue(t *testing.T) {
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
	release := make(chan struct{})
	c, err := OpenWithProber(db, ProberFunc(func(_ context.Context, _ *os.File) (MediaProperties, error) {
		entered <- struct{}{}
		<-release
		return MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.StartScanScheduler(t.Context(), 1) != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Shutdown(context.Background())
	job, err := c.QueueLibraryScan("films", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_scan_terminal BEFORE UPDATE OF status ON scan_jobs WHEN OLD.id=%q AND NEW.status='succeeded' BEGIN SELECT RAISE(FAIL,'injected terminal failure'); END`, job.ID)); err != nil {
		t.Fatal(err)
	}
	close(release)
	job = waitScanJob(t, c, job.ID)
	if job.Status != "failed" || !strings.Contains(job.Message, "could not be recorded") {
		t.Fatalf("terminal failure job = %#v", job)
	}
	if _, err := c.QueueLibraryScan("films", "manual"); err != nil {
		t.Fatalf("terminal failure permanently blocked the library: %v", err)
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

func TestScopedRetryPreservesKnownFileWhenItCannotBeAttempted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wantCode  string
		makeStale func(*testing.T, *Catalog, string)
	}{
		{
			name:     "deleted",
			wantCode: "not_found",
			makeStale: func(t *testing.T, _ *Catalog, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "excluded",
			wantCode: "excluded_by_policy",
			makeStale: func(t *testing.T, c *Catalog, _ string) {
				t.Helper()
				if _, err := c.SetScanPolicy("films", ScanPolicy{ScheduleKind: "interval", IntervalSeconds: 3600, Exclusions: []string{"film.mp4"}}); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "film.mp4")
			if err := os.WriteFile(path, []byte("media"), 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sqlite.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fail := false
			c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
				if fail {
					return MediaProperties{}, errors.New("temporary probe failure")
				}
				return MediaProperties{}, nil
			}))
			if err != nil || c.SetRoots(root, "") != nil || c.Scan(t.Context(), 1) != nil {
				t.Fatalf("initial scan: %v", err)
			}
			before, err := c.List("", 0, 10)
			if err != nil || len(before) != 1 {
				t.Fatalf("initial catalog: items=%#v err=%v", before, err)
			}
			if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			fail = true
			job, err := c.QueueLibraryScan("films", "manual")
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := c.claimScanJob(time.Now())
			if err != nil || claimed.ID != job.ID {
				t.Fatalf("claim failed job: %#v %v", claimed, err)
			}
			c.executeScanJob(t.Context(), claimed)
			failed, err := c.ScanJob(job.ID)
			if err != nil || failed.Status != "partial" || len(failed.Files) != 1 || !failed.Files[0].Retryable {
				t.Fatalf("failed job = %#v, %v", failed, err)
			}
			if _, err := db.Exec(`DELETE FROM scan_jobs WHERE retry_of=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			retry, err := c.RetryScanJob(job.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			tc.makeStale(t, c, path)
			claimed, err = c.claimScanJob(time.Now())
			if err != nil || claimed.ID != retry.ID {
				t.Fatalf("claim retry: %#v %v", claimed, err)
			}
			c.executeScanJob(t.Context(), claimed)
			retry, err = c.ScanJob(retry.ID)
			if err != nil || retry.Status == "succeeded" || len(retry.Files) != 1 || retry.Files[0].ErrorCode != tc.wantCode || retry.Files[0].Retryable {
				t.Fatalf("unattempted retry = %#v, %v", retry, err)
			}
			after, err := c.List("", 0, 10)
			if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
				t.Fatalf("known catalog item was erased: before=%#v after=%#v err=%v", before, after, err)
			}
		})
	}
}

func TestMissingFFprobeFailureIsPermanentForScheduledJob(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		return MediaProperties{}, errors.New("ffprobe unavailable")
	}))
	if err != nil || c.SetRoots(root, "") != nil {
		t.Fatal(err)
	}
	queued, err := c.QueueLibraryScan("films", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := c.claimScanJob(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c.executeScanJob(t.Context(), claimed)
	job, err := c.ScanJob(queued.ID)
	if err != nil || job.Status != "partial" || len(job.Files) != 1 || job.Files[0].ErrorCode != "ffprobe_unavailable" || job.Files[0].Retryable {
		t.Fatalf("ffprobe failure = %#v, %v", job, err)
	}
	jobs, err := c.ScanJobs(10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("permanent failure queued retries: %#v, %v", jobs, err)
	}
}

func TestDisablingPolicySerializesWithDueEnqueue(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetScanPolicy("films", ScanPolicy{Enabled: true, ScheduleKind: "interval", IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE library_scan_policies SET next_run_at=? WHERE library_id='films'`, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		errs <- c.enqueueDueScans(time.Now())
	}()
	go func() {
		<-start
		_, err := c.SetScanPolicy("films", ScanPolicy{Enabled: false, ScheduleKind: "interval", IntervalSeconds: 60})
		errs <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	policy, err := c.ScanPolicy("films")
	if err != nil || policy.Enabled || policy.NextRunAt != 0 {
		t.Fatalf("disabled policy was overwritten by due enqueue: %#v, %v", policy, err)
	}
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
