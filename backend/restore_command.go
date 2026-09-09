package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mopeyjellyfish/flixr/backend/backup"
	"github.com/mopeyjellyfish/flixr/backend/config"
)

func restoreBackup(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("restore-backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", config.DataDir(), "existing empty or Flixr data directory")
	archive := flags.String("archive", "", "verified Flixr backup archive")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *archive == "" {
		return errors.New("usage: flixr restore-backup [--data-dir DIR] --archive FILE")
	}
	target, err := filepath.Abs(*dataDir)
	if err != nil {
		return errors.New("invalid Flixr data directory")
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return errors.New("Flixr data directory must already exist and be accessible")
	}
	unlock, ok, err := acquireDataLock(filepath.Join(target, ".lock"))
	if err != nil {
		return fmt.Errorf("acquire Flixr data lock: %w", err)
	}
	if !ok {
		return errors.New("Flixr data directory is already in use; stop Flixr before restore")
	}
	defer unlock()
	result, err := backup.Restore(context.Background(), *archive, target)
	if err != nil {
		return err
	}
	if result.SafetyBackup != "" {
		fmt.Fprintln(output, "Backup restored. A verified pre-restore safety backup was retained.")
	} else {
		fmt.Fprintln(output, "Backup restored and verified.")
	}
	return nil
}
