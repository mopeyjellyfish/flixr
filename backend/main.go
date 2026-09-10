package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"

	"github.com/mopeyjellyfish/flixr/backend/backup"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/config"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/screens"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"github.com/spf13/afero"
)

var version = "dev"
var revision = "unknown"
var applicationTMDBToken string

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("Flixr %s (%s)\n", version, revision)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "recover-owner" {
		if err := recoverOwner(os.Args[2:], os.Stdout); err != nil {
			slog.Error("recover owner", "err", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "restore-backup" {
		if err := restoreBackup(os.Args[2:], os.Stdout); err != nil {
			slog.Error("restore backup", "err", err)
			os.Exit(1)
		}
		return
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load Flixr configuration", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		slog.Error("Flixr stopped", "err", err)
		os.Exit(1)
	}
}

var acquireDataLock = func(path string) (func() error, bool, error) {
	lock := flock.New(path)
	ok, err := lock.TryLock()
	return lock.Unlock, ok, err
}

func run(ctx context.Context, cfg config.Bootstrap) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	filesystem := afero.NewOsFs()
	if err := filesystem.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	unlock, ok, err := acquireDataLock(filepath.Join(cfg.DataDir, ".lock"))
	if err != nil {
		return fmt.Errorf("acquire Flixr data lock: %w", err)
	}
	if !ok {
		return errors.New("Flixr data directory is already in use")
	}
	defer unlock()
	segmentDir := cfg.SegmentDir
	if segmentDir == "" {
		var err error
		segmentDir, err = playback.LoadSegmentDir(filesystem, cfg.DataDir)
		if err != nil {
			return err
		}
	}
	if err := filesystem.MkdirAll(segmentDir, 0o700); err != nil {
		return fmt.Errorf("create playback segment directory: %w", err)
	}
	segmentUnlock := func() error { return nil }
	if !within(cfg.DataDir, segmentDir) {
		segmentUnlock, ok, err = acquireDataLock(filepath.Join(segmentDir, ".flixr.lock"))
		if err != nil {
			return fmt.Errorf("acquire playback segment lock: %w", err)
		}
		if !ok {
			return errors.New("Flixr playback segment directory is already in use")
		}
		defer segmentUnlock()
	}
	db, err := sqlite.OpenContext(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		return err
	}
	var c *catalog.Catalog
	if cfg.Demo {
		c, err = catalog.OpenDemo(db)
	} else {
		c, err = catalog.Open(db)
	}
	if err != nil {
		return err
	}
	if cfg.OwnerPassword != "" && !h.Claimed() {
		if _, err := h.Claim(h.SetupToken(), cfg.OwnerPassword); err != nil {
			return err
		}
	}
	if cfg.InitialProfile != "" && h.Claimed() && len(h.Profiles()) == 0 {
		if _, err := h.CreateProfile(cfg.InitialProfile, ""); err != nil {
			return err
		}
	}
	if cfg.FilmsRoot != nil || cfg.TVRoot != nil {
		films, tv := c.Roots()
		if cfg.FilmsRoot != nil {
			films = *cfg.FilmsRoot
		}
		if cfg.TVRoot != nil {
			tv = *cfg.TVRoot
		}
		if err := c.StageEnvironmentRoots(films, tv); err != nil {
			return fmt.Errorf("environment media roots: %w", err)
		}
	}
	if err := configureMetadata(c, cfg.Environment); err != nil {
		return err
	}
	if cfg.Demo && c.DemoSource() != "" {
		if err := prepareDemoHousehold(h, c); err != nil {
			return err
		}
	}
	settings, err := playback.LoadSettingsWithFilesystem(db, cfg.DataDir, filesystem)
	if err != nil {
		return err
	}
	settings, err = cfg.PlaybackSettings(settings)
	if err != nil {
		return fmt.Errorf("environment playback settings: %w", err)
	}
	inputListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start playback input listener: %w", err)
	}
	defer inputListener.Close()
	inputBase := "http://" + inputListener.Addr().String()
	p, err := playback.NewManager(playback.ManagerConfig{Settings: settings, DB: db, FS: filesystem, InputBase: inputBase, Executor: playback.OSExecutor{}, ManifestWait: 10 * time.Second})
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = p.Shutdown(shutdown)
	}()
	if !h.Claimed() {
		fmt.Printf("Flixr setup token: %s\n", h.SetupToken())
	}
	workers := cfg.ScanWorkers
	if workers == 0 {
		workers = 4
	}
	metadataRefresh := c.MetadataRefreshDue()
	if err := c.ApplyScanScheduleOverride(cfg.ScanSchedule); err != nil {
		return fmt.Errorf("apply scan schedule override: %w", err)
	}
	if metadataRefresh {
		if err := c.StartScan(ctx, workers); err != nil {
			return err
		}
	}
	if err := c.StartScanScheduler(ctx, workers); err != nil {
		return fmt.Errorf("start scan scheduler: %w", err)
	}
	if cfg.ScanOnStart && !metadataRefresh {
		if _, err := c.QueueAllLibraryScans("startup"); err != nil {
			return fmt.Errorf("queue startup scans: %w", err)
		}
	}
	screenManager := screens.New(time.Minute)
	mediaRoots, err := configuredMediaRoots(c)
	if err != nil {
		return fmt.Errorf("load media roots for backup confinement: %w", err)
	}
	backupManager, err := backup.NewManager(db, backup.Source{DB: db, DataDir: cfg.DataDir, MediaRoots: mediaRoots, MediaRootProvider: func() ([]string, error) { return configuredMediaRoots(c) }, SegmentDir: segmentDir, AppVersion: version})
	if err != nil {
		return fmt.Errorf("open backup manager: %w", err)
	}
	if err := applyBackupEnvironment(backupManager, cfg.Environment); err != nil {
		return fmt.Errorf("apply backup environment: %w", err)
	}
	backupManager.Start(ctx)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = backupManager.Shutdown(shutdown)
	}()
	webServer := web.NewServerWithConfigurationValues(h, c, p, screenManager, config.EnvironmentLocks(), cfg.NonSecretValues(), web.Build{Version: version, Revision: revision})
	webServer.AttachBackupManager(backupManager)
	application := webServer.Handler()
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: application, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	inputOnly := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/playback/input/"
		token := strings.TrimPrefix(r.URL.Path, prefix)
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || token == r.URL.Path || token == "" || strings.Contains(token, "/") {
			http.NotFound(w, r)
			return
		}
		application.ServeHTTP(w, r)
	})
	inputServer := &http.Server{Handler: inputOnly, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	errCh := make(chan error, 2)
	go func() { errCh <- inputServer.Serve(inputListener) }()
	go func() {
		if cfg.TLSCert != "" {
			errCh <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()
	var serveErr error
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Close upgraded screen connections before waiting for HTTP and media work.
	screenManager.Shutdown()
	serverErr := srv.Shutdown(shutdown)
	playbackErr := p.Shutdown(shutdown)
	inputErr := inputServer.Shutdown(shutdown)
	scanErr := c.Shutdown(shutdown)
	if err := errors.Join(serveErr, scanErr, playbackErr, inputErr, serverErr); err != nil {
		return fmt.Errorf("bounded shutdown: %w", err)
	}
	return nil
}

func configuredMediaRoots(c *catalog.Catalog) ([]string, error) {
	libraries, err := c.Libraries()
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, library := range libraries {
		for _, location := range library.Locations {
			if location.RootPath != "" {
				roots = append(roots, location.RootPath)
			}
		}
	}
	return roots, nil
}

func applyBackupEnvironment(manager *backup.Manager, environment config.Environment) error {
	if environment.BackupDestination == "" && environment.BackupSchedule == "" && environment.BackupRetainCount == 0 && environment.BackupRetainAge == 0 && environment.BackupBudgetBytes == 0 {
		return nil
	}
	policy, err := manager.Policy()
	if err != nil {
		return err
	}
	if environment.BackupDestination != "" {
		policy.Destination = environment.BackupDestination
	}
	if environment.BackupRetainCount > 0 {
		policy.RetainCount = environment.BackupRetainCount
	}
	if environment.BackupRetainAge > 0 {
		policy.RetainAgeSeconds = int64(environment.BackupRetainAge / time.Second)
	}
	if environment.BackupBudgetBytes > 0 {
		policy.BudgetBytes = environment.BackupBudgetBytes
	}
	policy, err = backup.ApplySchedule(policy, environment.BackupSchedule)
	if err != nil {
		return err
	}
	return manager.SetEnvironmentOverride(policy, config.EnvironmentLocks())
}

func configureMetadata(c *catalog.Catalog, environment config.Environment) error {
	c.SetApplicationTMDBToken(applicationTMDBToken)
	if environment.TMDBToken != nil {
		if err := c.SetTMDBToken(*environment.TMDBToken); err != nil {
			return fmt.Errorf("apply metadata credential override: %w", err)
		}
	}
	if environment.MetadataEnabled != nil {
		if err := c.SetMetadataEnabled(*environment.MetadataEnabled); err != nil {
			return fmt.Errorf("apply metadata enabled policy: %w", err)
		}
	}
	return nil
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// Only an explicit demo with a downloaded snapshot receives sample credentials/history.
func prepareDemoHousehold(h *household.Manager, c *catalog.Catalog) error {
	if !c.Demo() {
		return errors.New("sample household requires demo mode")
	}
	if !h.Claimed() {
		if _, err := h.Claim(h.SetupToken(), "flixr-demo-only"); err != nil {
			return err
		}
		fmt.Println("Flixr development demo owner password: flixr-demo-only")
	}
	if len(h.Profiles()) != 0 {
		return nil
	}
	items, _, err := c.Browse("", 0, 100)
	if err != nil {
		return err
	}
	for _, name := range []string{"Alex", "Sam", "Guest"} {
		pin := ""
		if name == "Sam" {
			pin = "2468"
		}
		profile, err := h.CreateProfile(name, pin)
		if err != nil {
			return err
		}
		for i, item := range items {
			if !item.Demo {
				continue
			}
			if i < 8 {
				if err := c.SetListed(profile.ID, item.Kind, item.ID, true); err != nil {
					return err
				}
			}
			if i < 4 {
				progressID := item.ID
				if item.Kind == "series" {
					show, ok := c.Series(item.ID)
					if !ok || len(show.Seasons) == 0 || len(show.Seasons[0].Episodes) == 0 {
						continue
					}
					progressID = show.Seasons[0].Episodes[0].ID
				}
				if err := h.ProgressForProfile(profile.ID, progressID, int64((i+1)*60000)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
