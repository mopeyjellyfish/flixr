package catalog

import (
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

// The policy subqueries are identical to the viewer page query. Explain both
// film and series branches so a schema change cannot silently drop the lookup
// indexes used when filtering before LIMIT.
func TestViewerPolicyQueryPlan(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	encoded, err := viewerPolicyArgument(access.Policy{LibraryIDs: []string{"restricted"}})
	if err != nil {
		t.Fatal(err)
	}
	query := `EXPLAIN QUERY PLAN SELECT q.id FROM (
		SELECT i.id,'film' AS kind,i.title,i.genres_json FROM catalog_items i WHERE i.series_id='' AND i.merged_into=''
		UNION ALL SELECT s.id,'series' AS kind,s.title,s.genres_json FROM catalog_series s WHERE s.merged_into=''
	) AS q WHERE (?='all' OR q.kind=?)` + viewerPolicySQL + ` ORDER BY LOWER(q.title),q.id,q.kind LIMIT ?`
	rows, err := db.Reader().QueryContext(t.Context(), query, "all", "all", encoded, 49)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	t.Logf("representative authorized film/series page plan:\n%s", plan)
	for _, index := range []string{"catalog_physical_files_catalog", "sqlite_autoindex_catalog_metadata_fields_1"} {
		if !strings.Contains(plan, index) {
			t.Errorf("viewer policy plan did not use %s", index)
		}
	}
}
