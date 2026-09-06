// Package config reads only bootstrap environment configuration.
package config

import (
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
)

type Bootstrap struct{ DataDir, ListenAddr, TLSCert, TLSKey string }

func Load() Bootstrap {
	d := os.Getenv("FLIXR_DATA_DIR")
	if d == "" {
		d = filepath.Join(".", "flixr-data")
	}
	a := os.Getenv("FLIXR_LISTEN_ADDR")
	if a == "" {
		a = "127.0.0.1:8787"
	}
	return Bootstrap{d, a, os.Getenv("FLIXR_TLS_CERT"), os.Getenv("FLIXR_TLS_KEY")}
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
