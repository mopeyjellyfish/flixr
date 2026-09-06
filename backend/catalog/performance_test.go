//go:build !race

package catalog_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestTenThousandItemBrowseBudget(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5_000 {
		if _, err := tx.Exec("INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,updated_at) VALUES(?,?,?,?,1,'film',0)", fmt.Sprintf("film-%d", i), "film", fmt.Sprintf("Film %05d", i), fmt.Sprintf("%d.mp4", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("INSERT INTO catalog_series(id,title,local_only,updated_at) VALUES(?,?,1,0)", fmt.Sprintf("series-%d", i), fmt.Sprintf("Series %05d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "series 049"} {
		t.Run(query, func(t *testing.T) {
			durations := make([]time.Duration, 0, 100)
			for range 100 {
				started := time.Now()
				items, total, err := c.Browse(query, 0, 50)
				if err != nil {
					t.Fatal(err)
				}
				if len(items) == 0 || total == 0 {
					t.Fatalf("browse %q returned %d of %d", query, len(items), total)
				}
				durations = append(durations, time.Since(started))
			}
			slices.Sort(durations)
			if p95 := durations[94]; p95 > 200*time.Millisecond {
				t.Fatalf("Browse p95 for %q took %s; budget is 200ms", query, p95)
			}
		})
	}
}
