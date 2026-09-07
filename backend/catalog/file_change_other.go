//go:build !linux && !darwin

package catalog

import "os"

func fileChangeToken(info os.FileInfo) string { return "" }
