//go:build darwin

package catalog

import (
	"fmt"
	"os"
	"syscall"
)

func fileChangeToken(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, stat.Ctimespec.Sec, stat.Ctimespec.Nsec)
	}
	return ""
}
