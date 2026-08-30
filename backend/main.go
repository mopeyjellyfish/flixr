package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gofrs/flock"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/config"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func main() {
	if err := run(context.Background(), config.Load()); err != nil {
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
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
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
	db, err := sqlite.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		return err
	}
	c, err := catalog.Open(db)
	if err != nil {
		return err
	}
	if !h.Claimed() {
		fmt.Printf("Flixr setup token: %s\n", h.SetupToken())
	}
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: web.NewServer(h, c).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" {
			errCh <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Stop accepting and drain requests before cancelling scanner work and closing SQLite.
	serverErr := srv.Shutdown(shutdown)
	scanErr := c.Shutdown(shutdown)
	if scanErr != nil || serverErr != nil {
		return fmt.Errorf("bounded shutdown: %w", errors.Join(scanErr, serverErr))
	}
	return nil
}
