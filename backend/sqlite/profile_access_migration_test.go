package sqlite_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/require"
)

func TestProfileAccessMigrationPreservesExistingProfilesAsUnrestricted(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TRIGGER profile_access_policy_insert; DROP TABLE profile_access_policies; DELETE FROM schema_migrations WHERE version=28; INSERT INTO profiles(id,name) VALUES('existing','Existing')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	var libraries, region, rating, unrated, allowTags, denyTags string
	var version int64
	err = db.QueryRow(`SELECT library_ids_json,rating_region,max_rating,unrated_policy,allow_tags_json,deny_tags_json,version FROM profile_access_policies WHERE profile_id='existing'`).Scan(&libraries, &region, &rating, &unrated, &allowTags, &denyTags, &version)
	require.NoError(t, err)
	require.Equal(t, "[]", libraries)
	require.Empty(t, region)
	require.Empty(t, rating)
	require.Equal(t, "allow", unrated)
	require.Equal(t, "[]", allowTags)
	require.Equal(t, "[]", denyTags)
	require.EqualValues(t, 1, version)
}
