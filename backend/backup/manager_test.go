package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestManagerPersistsPolicyRunsAndRestoresAutomaticBackup(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO settings(key,value) VALUES('automatic-proof','saved')`); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(db, Source{DB: db, DataDir: data, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.SetPolicy(Policy{Enabled: true, Destination: destination, ScheduleKind: "interval", IntervalSeconds: 3600, LocalTime: "03:00", Timezone: "UTC", RetainCount: 2, RetainAgeSeconds: 86400, BudgetBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || p.NextRunAt == 0 {
		t.Fatalf("policy not scheduled: %+v", p)
	}
	job, err := m.Queue("manual")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err = m.Job(job.ID)
		if err == nil && job.Status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != "succeeded" {
		t.Fatalf("job = %+v, %v", job, err)
	}
	target := t.TempDir()
	archive := filepath.Join(destination, job.ArchiveName)
	if _, err := Restore(context.Background(), archive, target); err != nil {
		t.Fatal(err)
	}
	restored, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.QueryRow(`SELECT value FROM settings WHERE key='automatic-proof'`).Scan(&value); err != nil || value != "saved" {
		t.Fatalf("restored value %q, %v", value, err)
	}
	loaded, err := m.Policy()
	if err != nil || loaded.Destination != destination || loaded.LastVerifiedAt == 0 {
		t.Fatalf("persisted policy = %+v, %v", loaded, err)
	}
}

func TestManagerRestartMarksWorkInterruptedAndCleansOwnedPartials(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE backup_policy SET destination=? WHERE id=1`, destination); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, ownerMarker), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(destination, ".flixr-backup-owned.partial")
	unrelated := filepath.Join(destination, "other.partial")
	os.WriteFile(partial, []byte("x"), 0o600)
	os.WriteFile(unrelated, []byte("x"), 0o600)
	if _, err = db.Exec(`INSERT INTO backup_jobs(id,trigger,status,queued_at) VALUES('stale','manual','running',1)`); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(db, Source{DB: db, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	job, err := m.Job("stale")
	if err != nil || job.Status != "interrupted" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("owned partial remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated partial removed: %v", err)
	}
}

func TestEnvironmentOverrideWinsWithoutReplacingSavedPolicy(t *testing.T) {
	data := t.TempDir()
	saved := t.TempDir()
	environment := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := NewManager(db, Source{DB: db, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	base := Policy{Destination: saved, ScheduleKind: "interval", IntervalSeconds: 86400, LocalTime: "03:00", Timezone: "UTC", RetainCount: 7, RetainAgeSeconds: 2592000, BudgetBytes: 1 << 30}
	if _, err = m.SetPolicy(base); err != nil {
		t.Fatal(err)
	}
	override := base
	override.Destination = environment
	override.Enabled = true
	override.IntervalSeconds = 3600
	if err = m.SetEnvironmentOverride(override, map[string]bool{"backup.destination": true, "backup.schedule": true}); err != nil {
		t.Fatal(err)
	}
	effective, err := m.Policy()
	if err != nil || effective.Destination != environment || !effective.Enabled {
		t.Fatalf("effective=%+v err=%v", effective, err)
	}
	if _, err = m.Queue("manual"); err != nil {
		t.Fatal(err)
	}
	m.runOnce(context.Background())
	effective, err = m.Policy()
	if err != nil || effective.NextRunAt == 0 {
		t.Fatalf("environment schedule lost after run: %+v %v", effective, err)
	}
	requested := effective
	requested.Destination = t.TempDir()
	requested.Enabled = false
	if _, err = m.SetPolicy(requested); err != nil {
		t.Fatal(err)
	}
	stored, err := m.storedPolicy()
	if err != nil || stored.Destination != saved || stored.Enabled {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestFailedBackupPreservesLastVerifiedState(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := NewManager(db, Source{DB: db, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{Destination: destination, ScheduleKind: "interval", IntervalSeconds: 3600, LocalTime: "03:00", Timezone: "UTC", RetainCount: 2, RetainAgeSeconds: 86400, BudgetBytes: 1 << 30}
	if _, err = m.SetPolicy(p); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Queue("manual"); err != nil {
		t.Fatal(err)
	}
	m.runOnce(context.Background())
	before, err := m.Policy()
	if err != nil || before.LastVerifiedAt == 0 {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	if err = os.Remove(filepath.Join(destination, ownerMarker)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(destination, "unrelated"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Queue("manual"); err != nil {
		t.Fatal(err)
	}
	m.runOnce(context.Background())
	after, err := m.Policy()
	if err != nil {
		t.Fatal(err)
	}
	if after.LastVerifiedAt != before.LastVerifiedAt || after.LastVerifiedFile != before.LastVerifiedFile || after.LastStatus != "failed" {
		t.Fatalf("after=%+v before=%+v", after, before)
	}
}

func TestCancellingQueuedJobDoesNotCancelRunningContext(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := NewManager(db, Source{DB: db, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{Destination: destination, ScheduleKind: "interval", IntervalSeconds: 3600, LocalTime: "03:00", Timezone: "UTC", RetainCount: 2, RetainAgeSeconds: 86400, BudgetBytes: 1 << 30}
	if _, err = m.SetPolicy(p); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO backup_jobs(id,trigger,status,queued_at) VALUES('running','manual','running',1),('queued','manual','queued',2)`); err != nil {
		t.Fatal(err)
	}
	cancelled := false
	m.cancel = func() { cancelled = true }
	job, err := m.Cancel("queued")
	if err != nil || job.Status != "cancelled" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if cancelled {
		t.Fatal("queued cancellation cancelled running work")
	}
}

func TestCancelWinningQueuedJobClaimIsNotOverwritten(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := NewManager(db, Source{DB: db, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{Destination: destination, ScheduleKind: "interval", IntervalSeconds: 3600, LocalTime: "03:00", Timezone: "UTC", RetainCount: 2, RetainAgeSeconds: 86400, BudgetBytes: 1 << 30}
	if _, err = m.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	job, err := m.Queue("manual")
	if err != nil {
		t.Fatal(err)
	}
	selected := make(chan struct{})
	resume := make(chan struct{})
	m.afterSelect = func(string) {
		close(selected)
		<-resume
	}
	done := make(chan struct{})
	go func() {
		m.runOnce(context.Background())
		close(done)
	}()
	<-selected
	resumed := false
	defer func() {
		if !resumed {
			close(resume)
		}
	}()
	cancelled, err := m.Cancel(job.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancelled job=%+v err=%v", cancelled, err)
	}
	close(resume)
	resumed = true
	<-done
	final, err := m.Job(job.ID)
	if err != nil || final.Status != "cancelled" || final.ArchiveName != "" {
		t.Fatalf("final job=%+v err=%v", final, err)
	}
	archives, err := filepath.Glob(filepath.Join(destination, "*"+archiveSuffix))
	if err != nil || len(archives) != 0 {
		t.Fatalf("cancelled job published archives=%v err=%v", archives, err)
	}
}

func TestCancelRunningBackupPersistsCancelledAndCleansPartials(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	objects := filepath.Join(data, "artwork", "objects")
	if err = os.MkdirAll(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(objects, "cancel-proof"), []byte("artwork"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO catalog_artwork(catalog_id,kind,content_type,object_name) VALUES('cancel-film','poster','image/jpeg','cancel-proof')`); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(db, Source{DB: db, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{Destination: destination, ScheduleKind: "interval", IntervalSeconds: 3600, LocalTime: "03:00", Timezone: "UTC", RetainCount: 2, RetainAgeSeconds: 86400, BudgetBytes: 1 << 30}
	if _, err = m.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	job, err := m.Queue("manual")
	if err != nil {
		t.Fatal(err)
	}
	copyReached := make(chan struct{})
	resume := make(chan struct{})
	m.beforeArtwork = func(string) {
		close(copyReached)
		<-resume
	}
	done := make(chan struct{})
	go func() {
		m.runOnce(context.Background())
		close(done)
	}()
	<-copyReached
	resumed := false
	defer func() {
		if !resumed {
			close(resume)
		}
	}()
	running, err := m.Job(job.ID)
	if err != nil || running.Status != "running" {
		t.Fatalf("running job=%+v err=%v", running, err)
	}
	if _, err = m.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	close(resume)
	resumed = true
	<-done
	final, err := m.Job(job.ID)
	if err != nil || final.Status != "cancelled" || final.Message != "Cancelled by owner." {
		t.Fatalf("final job=%+v err=%v", final, err)
	}
	partials, err := filepath.Glob(filepath.Join(destination, ".flixr-*.partial"))
	if err != nil || len(partials) != 0 {
		t.Fatalf("cancelled backup partials=%v err=%v", partials, err)
	}
	archives, err := filepath.Glob(filepath.Join(destination, "*"+archiveSuffix))
	if err != nil || len(archives) != 0 {
		t.Fatalf("cancelled backup archives=%v err=%v", archives, err)
	}
}
