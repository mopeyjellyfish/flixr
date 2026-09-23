package catalog

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/access"
	modernsqlite "modernc.org/sqlite"
)

// The catalog query supplies policy inputs from the same source tables as
// AccessContent. Evaluating the shared policy before LIMIT keeps restricted
// pages bounded without acquiring a second reader while rows are open.
func init() {
	modernsqlite.MustRegisterScalarFunction("flixr_viewer_allowed", 5, viewerAllowed)
}

func viewerAllowed(_ *modernsqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	text := func(index int) (string, error) {
		if args[index] == nil {
			return "", nil
		}
		value, ok := args[index].(string)
		if !ok {
			return "", fmt.Errorf("viewer policy input %d is not text", index)
		}
		return value, nil
	}
	policyJSON, err := text(0)
	if err != nil {
		return nil, err
	}
	var policy access.Policy
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
		return nil, fmt.Errorf("decode viewer policy: %w", err)
	}
	librariesJSON, err := text(2)
	if err != nil {
		return nil, err
	}
	genresJSON, err := text(1)
	if err != nil {
		return nil, err
	}
	var content access.Content
	if err := json.Unmarshal([]byte(librariesJSON), &content.LibraryIDs); err != nil {
		return nil, fmt.Errorf("decode viewer libraries: %w", err)
	}
	if err := json.Unmarshal([]byte(genresJSON), &content.Tags); err != nil {
		return nil, fmt.Errorf("decode viewer genres: %w", err)
	}
	content.Rating, err = text(4)
	if err != nil {
		return nil, err
	}
	tags, err := text(3)
	if err != nil {
		return nil, err
	}
	for _, tag := range strings.Split(tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			content.Tags = append(content.Tags, tag)
		}
	}
	if policy.Allows(content) {
		return int64(1), nil
	}
	return int64(0), nil
}

// q is the union of canonical film and series titles in the caller. A title
// with no present physical source has no library IDs, matching AccessContent.
const viewerPolicySQL = ` AND flixr_viewer_allowed(?, q.genres_json,
	CASE WHEN q.kind='film' THEN
		(SELECT json_group_array(DISTINCT x.library_id) FROM catalog_physical_files f JOIN library_locations x ON x.id=f.location_id WHERE f.catalog_id=q.id AND f.present=1)
	ELSE
		(SELECT json_group_array(DISTINCT x.library_id) FROM catalog_items i JOIN catalog_physical_files f ON f.catalog_id=i.id JOIN library_locations x ON x.id=f.location_id WHERE i.series_id=q.id AND i.merged_into='' AND f.present=1)
	END,
	(SELECT value FROM catalog_metadata_fields WHERE catalog_kind=q.kind AND catalog_id=q.id AND field='tags'),
	(SELECT value FROM catalog_metadata_fields WHERE catalog_kind=q.kind AND catalog_id=q.id AND field='content_rating'))=1`

func viewerPolicyArgument(policy access.Policy) (string, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode viewer policy: %w", err)
	}
	return string(data), nil
}
