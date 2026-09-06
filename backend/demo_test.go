package main

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/require"
)

func TestSampleHouseholdRequiresDemoAndPreservesExistingProfiles(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	h, err := household.Open(db)
	require.NoError(t, err)
	normal, err := catalog.Open(db)
	require.NoError(t, err)
	require.Error(t, prepareDemoHousehold(h, normal))
	require.False(t, h.Claimed())
	demo, err := catalog.OpenDemo(db)
	require.NoError(t, err)
	require.NoError(t, prepareDemoHousehold(h, demo))
	require.True(t, h.Claimed())
	profiles := h.Profiles()
	require.Len(t, profiles, 3)
	require.NoError(t, prepareDemoHousehold(h, demo))
	require.Equal(t, profiles, h.Profiles())
	viewer, err := demo.Viewer(profiles[0].ID, "all")
	require.NoError(t, err)
	require.NotEmpty(t, viewer.Sections[0].Items)
	require.NotEmpty(t, viewer.Sections[2].Items)
}
