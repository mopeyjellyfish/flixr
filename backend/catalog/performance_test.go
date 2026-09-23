//go:build !race

package catalog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/household"

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

func TestBoundedViewerPayloadAtCatalogScale(t *testing.T) {
	for _, size := range []int{10_000, 100_000} {
		t.Run(fmt.Sprintf("%d titles", size), func(t *testing.T) {
			db, err := sqlite.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			statement, err := tx.Prepare(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,updated_at,genres_json,added_at) VALUES(?, 'film', ?, ?, 1, 'film', 0, '["Drama"]', ?)`)
			if err != nil {
				t.Fatal(err)
			}
			for index := range size {
				id := fmt.Sprintf("film-%06d", index)
				if _, err := statement.Exec(id, fmt.Sprintf("Film %06d", index), id+".mp4", index); err != nil {
					t.Fatal(err)
				}
				if index < 100 && index%2 == 0 {
					if _, err := tx.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('film',?,'tags','denied','local',1)`, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := statement.Close(); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			library, err := catalog.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			house, err := household.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := house.CreateProfile("Scale", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := library.SavePreference(profile.ID, "film", catalog.ViewPreference{View: "grid", Sort: "title"}); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			view, err := library.ViewerPage(context.Background(), profile.ID, "film", "", "", 48, access.Unrestricted())
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Items) != 48 || view.NextCursor == "" {
				t.Fatalf("got %d items and cursor %t", len(view.Items), view.NextCursor != "")
			}
			if len(payload) > 64*1024 {
				t.Fatalf("bounded viewer payload is %d bytes", len(payload))
			}
			t.Logf("fixture_titles=%d response_items=%d response_bytes=%d latency=%s", size, len(view.Items), len(payload), time.Since(started))
			restrictedStarted := time.Now()
			restricted, err := library.ViewerPage(context.Background(), profile.ID, "film", "", "", 48, access.Policy{DenyTags: []string{"denied"}})
			if err != nil {
				t.Fatal(err)
			}
			restrictedPayload, err := json.Marshal(restricted)
			if err != nil {
				t.Fatal(err)
			}
			if len(restricted.Items) != 48 || restricted.NextCursor == "" || restricted.Items[0].ID != "film-000001" {
				t.Fatalf("restricted page got %d items, first %q, and cursor %t", len(restricted.Items), restricted.Items[0].ID, restricted.NextCursor != "")
			}
			if len(restrictedPayload) > 64*1024 {
				t.Fatalf("bounded restricted viewer payload is %d bytes", len(restrictedPayload))
			}
			t.Logf("fixture_titles=%d restricted_response_items=%d restricted_response_bytes=%d restricted_latency=%s", size, len(restricted.Items), len(restrictedPayload), time.Since(restrictedStarted))
			allDeniedStarted := time.Now()
			allDenied, err := library.ViewerPage(context.Background(), profile.ID, "film", "", "", 48, access.Policy{LibraryIDs: []string{"not-a-library"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(allDenied.Items) != 0 || allDenied.NextCursor != "" {
				t.Fatalf("all-denied view contained %d titles or a cursor", len(allDenied.Items))
			}
			elapsed := time.Since(allDeniedStarted)
			t.Logf("fixture_titles=%d all_denied_response_items=%d all_denied_latency=%s", size, len(allDenied.Items), elapsed)
			if elapsed > 10*time.Second {
				t.Fatalf("all-denied %d-title page took %s; must not issue a policy lookup for every title", size, elapsed)
			}
		})
	}
}

func TestViewerPageHonorsCanceledContext(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("Canceled", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = library.ViewerPage(ctx, profile.ID, "all", "", "", 48, access.Unrestricted())
	if err == nil {
		t.Fatal("canceled viewer query succeeded")
	}
}
