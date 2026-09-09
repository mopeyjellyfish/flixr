package household_test

import (
	"encoding/json"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExistingAndNewProfilesDefaultToUnrestrictedPolicyAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	h, err := household.Open(db)
	require.NoError(t, err)
	p, err := h.CreateProfile("Viewer", "")
	require.NoError(t, err)
	policy, err := h.Policy(p.ID)
	require.NoError(t, err)
	assert.True(t, policy.Allows(access.Content{}))
	assert.EqualValues(t, 1, policy.Version)
	encoded, err := json.Marshal(policy)
	require.NoError(t, err)
	assert.JSONEq(t, `{"library_ids":[],"unrated_policy":"allow","allow_tags":[],"deny_tags":[],"version":1}`, string(encoded))
	require.NoError(t, db.Close())

	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	h, err = household.Open(db)
	require.NoError(t, err)
	policy, err = h.Policy(p.ID)
	require.NoError(t, err)
	assert.True(t, policy.Allows(access.Content{}))
}

func TestPolicyUpdatePersistsAndRevokesProfileSessions(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	h, err := household.Open(db)
	require.NoError(t, err)
	p, err := h.CreateProfile("Viewer", "")
	require.NoError(t, err)
	one, err := h.Select(p.ID, "")
	require.NoError(t, err)
	two, err := h.Select(p.ID, "")
	require.NoError(t, err)

	policy, revoked, err := h.UpdatePolicy(p.ID, access.Policy{LibraryIDs: []string{"kids"}, RatingRegion: "GB", MaxRating: "12", Unrated: access.UnratedDeny, AllowTags: []string{"family"}, DenyTags: []string{"scary"}})
	require.NoError(t, err)
	assert.EqualValues(t, 2, policy.Version)
	assert.Len(t, revoked, 2)
	_, oneActive := h.Profile(one)
	_, twoActive := h.Profile(two)
	assert.False(t, oneActive)
	assert.False(t, twoActive)

	reopened, err := household.Open(db)
	require.NoError(t, err)
	policy, err = reopened.Policy(p.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"kids"}, policy.LibraryIDs)
	assert.Equal(t, "12", policy.MaxRating)
}

func TestFailedPolicyUpdateKeepsPolicyAndSessions(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	h, err := household.Open(db)
	require.NoError(t, err)
	p, err := h.CreateProfile("Viewer", "")
	require.NoError(t, err)
	token, err := h.Select(p.ID, "")
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER reject_policy_update BEFORE UPDATE ON profile_access_policies BEGIN SELECT RAISE(ABORT, 'interrupted'); END`)
	require.NoError(t, err)

	_, revoked, err := h.UpdatePolicy(p.ID, access.Policy{LibraryIDs: []string{"kids"}})
	require.Error(t, err)
	assert.Empty(t, revoked)
	_, active := h.Profile(token)
	assert.True(t, active)
	policy, err := h.Policy(p.ID)
	require.NoError(t, err)
	assert.Empty(t, policy.LibraryIDs)
}
