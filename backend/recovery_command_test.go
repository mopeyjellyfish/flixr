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

func TestRecoverOwnerCommandRejectsLockedDataDirectoryWithoutChangingOwner(t *testing.T) {
	dir, secret := claimedRecoveryStore(t)
	unlock, ok, err := acquireDataLock(filepath.Join(dir, ".lock"))
	require.NoError(t, err)
	require.True(t, ok)
	defer unlock()

	require.Error(t, recoverOwner([]string{"--data-dir", dir, "--password-file", secret}, &bytes.Buffer{}))
	assertOwnerPassword(t, dir, "old password")
}

func TestRecoverOwnerCommandRejectsInvalidPasswordFilesWithoutChangingOwner(t *testing.T) {
	for _, password := range [][]byte{nil, make([]byte, 16385)} {
		t.Run("invalid password file", func(t *testing.T) {
			dir, secret := claimedRecoveryStore(t)
			require.NoError(t, os.WriteFile(secret, password, 0o600))
			require.Error(t, recoverOwner([]string{"--data-dir", dir, "--password-file", secret}, &bytes.Buffer{}))
			assertOwnerPassword(t, dir, "old password")
		})
	}
}

func TestRecoverOwnerCommandRejectsMissingDatabaseWithoutCreatingOne(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "replacement-password")
	require.NoError(t, os.WriteFile(secret, []byte("new password"), 0o600))
	require.Error(t, recoverOwner([]string{"--data-dir", dir, "--password-file", secret}, &bytes.Buffer{}))
	_, err := os.Stat(filepath.Join(dir, "flixr.db"))
	assert.True(t, os.IsNotExist(err))
}

func TestRecoverOwnerCommandRejectsUnclaimedStoreWithoutCredentialOrAuditMutation(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	secret := filepath.Join(dir, "replacement-password")
	require.NoError(t, os.WriteFile(secret, []byte("new password"), 0o600))

	require.Error(t, recoverOwner([]string{"--data-dir", dir, "--password-file", secret}, &bytes.Buffer{}))
	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	var owners, audits int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM owner").Scan(&owners))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&audits))
	assert.Zero(t, owners)
	assert.Zero(t, audits)
}

func claimedRecoveryStore(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	h, err := household.Open(db)
	require.NoError(t, err)
	_, err = h.Claim(h.SetupToken(), "old password")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	secret := filepath.Join(dir, "replacement-password")
	require.NoError(t, os.WriteFile(secret, []byte("new password"), 0o600))
	return dir, secret
}

func assertOwnerPassword(t *testing.T, dir, password string) {
	t.Helper()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	h, err := household.Open(db)
	require.NoError(t, err)
	_, err = h.Login(password)
	assert.NoError(t, err)
}
