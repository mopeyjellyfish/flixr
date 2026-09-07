// Package config reads only bootstrap environment configuration.
package config

import (
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

type Bootstrap struct {
	Environment
	DataDir, ListenAddr, TLSCert, TLSKey string
	Demo                                 bool
}

func Load() (Bootstrap, error) {
	d := DataDir()
	a := os.Getenv("FLIXR_LISTEN_ADDR")
	if a == "" {
		a = "127.0.0.1:8787"
	}
	demo, err := strconv.ParseBool(os.Getenv("FLIXR_DEMO"))
	if os.Getenv("FLIXR_DEMO") != "" && err != nil {
		return Bootstrap{}, err
	}
	environment, err := loadEnvironment()
	if err != nil {
		return Bootstrap{}, err
	}
	return Bootstrap{Environment: environment, DataDir: d, ListenAddr: a, TLSCert: os.Getenv("FLIXR_TLS_CERT"), TLSKey: os.Getenv("FLIXR_TLS_KEY"), Demo: demo}, nil
}

func DataDir() string {
	if d := os.Getenv("FLIXR_DATA_DIR"); d != "" {
		return d
	}
	return filepath.Join(".", "flixr-data")
}

// Validate rejects incomplete or unreadable TLS configuration before serving.
func (b Bootstrap) Validate() error {
	if (b.TLSCert == "") != (b.TLSKey == "") {
		return errors.New("both FLIXR_TLS_CERT and FLIXR_TLS_KEY are required for TLS")
	}
	if b.TLSCert != "" {
		if _, err := tls.LoadX509KeyPair(b.TLSCert, b.TLSKey); err != nil {
			return err
		}
	}
	return nil
}
