package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverOwnerCommandUsesLocalPasswordFile(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	h, err := household.Open(db)
	require.NoError(t, err)
	_, err = h.Claim(h.SetupToken(), "old password")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	secret := filepath.Join(dir, "replacement-password")
	require.NoError(t, os.WriteFile(secret, []byte("new password\n"), 0o600))

	var output bytes.Buffer
	require.NoError(t, recoverOwner([]string{"--data-dir", dir, "--password-file", secret}, &output))
	assert.Contains(t, output.String(), "Owner credential recovered")
	assert.NotContains(t, output.String(), "new password")

	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	h, err = household.Open(db)
	require.NoError(t, err)
	_, err = h.Login("new password")
	assert.NoError(t, err)
}

func TestRecoverOwnerCommandRejectsMissingPasswordFileWithoutChangingOwner(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	h, err := household.Open(db)
	require.NoError(t, err)
	_, err = h.Claim(h.SetupToken(), "old password")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = recoverOwner([]string{"--data-dir", dir, "--password-file", filepath.Join(dir, "missing")}, &bytes.Buffer{})
	require.Error(t, err)
	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	h, err = household.Open(db)
	require.NoError(t, err)
	_, err = h.Login("old password")
	assert.NoError(t, err)
}
