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

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/config"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/screens"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"github.com/spf13/afero"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load Flixr configuration", "err", err)
		os.Exit(1)
	}
	if err := run(context.Background(), cfg); err != nil {
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
	segmentDir, err := playback.LoadSegmentDir(filesystem, cfg.DataDir)
	if err != nil {
		return err
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
	db, err := sqlite.Open(cfg.DataDir)
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
	settings, err := playback.LoadSettingsWithFilesystem(db, cfg.DataDir, filesystem)
	if err != nil {
		return err
	}
	inputListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start playback input listener: %w", err)
	}
	defer inputListener.Close()
	inputBase := "http://" + inputListener.Addr().String()
	p, err := playback.NewManager(playback.ManagerConfig{Settings: settings, DB: db, FS: filesystem, InputBase: inputBase, Executor: playback.OSExecutor{}, SaveProgress: h.ProgressForProfile, ManifestWait: 10 * time.Second})
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
	screenManager := screens.New(time.Minute)
	application := web.NewServerWithScreens(h, c, p, screenManager).Handler()
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
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
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

func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
