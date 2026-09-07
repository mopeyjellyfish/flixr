package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/config"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func recoverOwner(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("recover-owner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", config.DataDir(), "existing Flixr data directory")
	passwordFile := flags.String("password-file", "", "file containing the replacement owner password")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *passwordFile == "" {
		return errors.New("usage: flixr recover-owner [--data-dir DIR] --password-file FILE")
	}
	info, err := os.Stat(*dataDir)
	if err != nil || !info.IsDir() {
		return errors.New("Flixr data directory must already exist and be accessible")
	}
	password, err := recoverySecret(*passwordFile)
	if err != nil {
		return err
	}
	unlock, ok, err := acquireDataLock(filepath.Join(*dataDir, ".lock"))
	if err != nil {
		return fmt.Errorf("acquire Flixr data lock: %w", err)
	}
	if !ok {
		return errors.New("Flixr data directory is already in use; stop Flixr before recovery")
	}
	defer unlock()
	db, err := sqlite.Open(*dataDir)
	if err != nil {
		return fmt.Errorf("open Flixr data: %w", err)
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		return fmt.Errorf("open household: %w", err)
	}
	if err := h.RecoverOwner(password); err != nil {
		return fmt.Errorf("recover owner: %w", err)
	}
	fmt.Fprintln(output, "Owner credential recovered; existing owner sessions were revoked.")
	return nil
}

func recoverySecret(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("cannot read password file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("password file must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 {
		return "", errors.New("password file is invalid or oversized")
	}
	password := strings.TrimRight(string(data), "\r\n")
	if password == "" {
		return "", errors.New("password file must not be empty")
	}
	return password, nil
}
