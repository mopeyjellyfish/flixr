package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

type blockedRootFilesystem struct {
	afero.Fs
	target  string
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockedRootFilesystem) Stat(name string) (os.FileInfo, error) {
	if name != f.target {
		return f.Fs.Stat(name)
	}
	blocked := false
	f.once.Do(func() {
		blocked = true
		close(f.started)
	})
	if blocked {
		<-f.release
	}
	return nil, errors.New("injected root read failure")
}

func TestSaveStatusRetainsCurrentAndThirtyOneRecentScanReports(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 40 {
		id := fmt.Sprintf("run-%02d", i)
		if _, err := db.Exec("INSERT INTO scan_runs(id,started_at,finished_at,status) VALUES(?,100,101,'complete')", id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO scan_files(scan_id,relative_path,outcome,message) VALUES(?,?,'matched','provider detail')", id, id+".mkv"); err != nil {
			t.Fatal(err)
		}
	}
	c := &Catalog{db: db}
	c.saveStatus(ScanStatus{ID: "current", StartedAt: 100, Status: "running"})

	var runs int
	if err := db.QueryRow("SELECT count(*) FROM scan_runs").Scan(&runs); err != nil || runs != 32 {
		t.Fatalf("retained scan reports = %d err=%v", runs, err)
	}
	for _, id := range []string{"current", "run-39", "run-09"} {
		var found int
		if err := db.QueryRow("SELECT count(*) FROM scan_runs WHERE id=?", id).Scan(&found); err != nil || found != 1 {
			t.Fatalf("retained %s = %d err=%v", id, found, err)
		}
	}
	var files int
	if err := db.QueryRow("SELECT count(*) FROM scan_files WHERE scan_id='run-39'").Scan(&files); err != nil || files != 1 {
		t.Fatalf("latest provider detail = %d err=%v", files, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM scan_files WHERE scan_id='run-08'").Scan(&files); err != nil || files != 0 {
		t.Fatalf("expired provider detail = %d err=%v", files, err)
	}
}

func TestSaveStatusFailureDoesNotPruneExistingScanReports(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 33 {
		if _, err := db.Exec("INSERT INTO scan_runs(id,started_at,finished_at,status) VALUES(?,?,101,'complete')", fmt.Sprintf("run-%02d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_current_scan BEFORE INSERT ON scan_runs WHEN NEW.id='current' BEGIN SELECT RAISE(ABORT, 'injected upsert failure'); END`); err != nil {
		t.Fatal(err)
	}
	c := &Catalog{db: db}
	c.saveStatus(ScanStatus{ID: "current", StartedAt: 100, Status: "running"})

	var runs int
	if err := db.QueryRow("SELECT count(*) FROM scan_runs").Scan(&runs); err != nil || runs != 33 {
		t.Fatalf("reports after failed upsert = %d err=%v", runs, err)
	}
}

func TestTerminalStatusIsPersistedBeforeAnotherScanIsAdmitted(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const root = "/blocked-film-root"
	fs := &blockedRootFilesystem{
		Fs:      afero.NewMemMapFs(),
		target:  root,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	c, err := OpenWithFilesystem(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		return MediaProperties{}, nil
	}), fs)
	if err != nil {
		t.Fatal(err)
	}
	c.film = root

	if err := c.StartScan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	firstID := c.ScanStatus().ID
	select {
	case <-fs.started:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not reach the controlled filesystem failure")
	}

	blocker, err := db.Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	close(fs.release)

	deadline := time.After(2 * time.Second)
	for c.ScanStatus().Status == "running" {
		select {
		case <-deadline:
			_ = blocker.Rollback()
			t.Fatal("scan did not reach terminal status")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	startResult := make(chan error, 1)
	go func() { startResult <- c.StartScan(context.Background(), 1) }()
	var (
		startErr error
		admitted bool
	)
	deadline = time.After(2 * time.Second)
observeAdmission:
	for {
		select {
		case startErr = <-startResult:
			break observeAdmission
		case <-deadline:
			_ = blocker.Rollback()
			t.Fatal("second scan admission did not resolve")
		default:
			if c.ScanStatus().ID != firstID {
				admitted = true
				break observeAdmission
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if admitted {
		select {
		case startErr = <-startResult:
		case <-time.After(2 * time.Second):
			t.Fatal("admitted scan did not resume after releasing the writer")
		}
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if admitted {
		t.Fatalf("second scan was admitted before %s reached durable terminal status", firstID)
	}
	if !errors.Is(startErr, ErrScanActive) {
		t.Fatalf("second StartScan error = %v, want %v", startErr, ErrScanActive)
	}
}
