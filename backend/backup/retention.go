package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Retention struct {
	Count       int
	Age         time.Duration
	BudgetBytes int64
}

type retainedArchive struct {
	path    string
	info    os.FileInfo
	created time.Time
}

func Rotate(ctx context.Context, destination string, policy Retention, now time.Time) error {
	if policy.Count < 1 || policy.Age < 0 || policy.BudgetBytes < 1 {
		return errors.New("backup retention policy is invalid")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return errors.New("backup destination cannot be read for retention")
	}
	valid := make([]retainedArchive, 0)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "flixr-backup-") || !strings.HasSuffix(entry.Name(), archiveSuffix) {
			continue
		}
		path := filepath.Join(destination, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		manifest, err := Verify(ctx, path)
		if err != nil {
			continue
		}
		valid = append(valid, retainedArchive{path, info, manifest.CreatedAt})
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].created.After(valid[j].created) })
	var used int64
	for i, item := range valid {
		used += item.info.Size()
		if i == 0 {
			continue
		}
		overCount := i >= policy.Count
		overAge := policy.Age > 0 && now.Sub(item.created) > policy.Age
		overBudget := used > policy.BudgetBytes
		if overCount || overAge || overBudget {
			if err := os.Remove(item.path); err != nil {
				return errors.New("expired backup could not be removed")
			}
			used -= item.info.Size()
		}
	}
	return syncDirectory(destination)
}
