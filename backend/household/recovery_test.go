package household

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverOwnerRotatesCredentialRevokesOwnerSessionsAndAudits(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	owner, err := m.Claim(m.SetupToken(), "old password")
	require.NoError(t, err)
	secondOwner, err := m.Login("old password")
	require.NoError(t, err)
	profile, err := m.CreateProfile("Ada", "")
	require.NoError(t, err)
	profileSession, err := m.Select(profile.ID, "")
	require.NoError(t, err)

	require.NoError(t, m.RecoverOwner("new password"))
	assert.False(t, m.Owner(owner))
	assert.False(t, m.Owner(secondOwner))
	selected, ok := m.Profile(profileSession)
	assert.True(t, ok)
	assert.Equal(t, profile.ID, selected.ID)
	_, err = m.Login("old password")
	assert.ErrorIs(t, err, ErrCredentials)
	newOwner, err := m.Login("new password")
	require.NoError(t, err)
	assert.True(t, m.Owner(newOwner))
	var events int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='owner_recovered'").Scan(&events))
	assert.Equal(t, 1, events)

	m, err = Open(db)
	require.NoError(t, err)
	assert.False(t, m.Owner(owner))
	_, err = m.Login("new password")
	assert.NoError(t, err)
}

func TestRecoverOwnerFailureKeepsExistingCredentialAndSessions(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	owner, err := m.Claim(m.SetupToken(), "old password")
	require.NoError(t, err)
	_, err = db.Exec("CREATE TRIGGER reject_owner_recovery BEFORE UPDATE ON owner BEGIN SELECT RAISE(ABORT, 'interrupted'); END")
	require.NoError(t, err)

	require.Error(t, m.RecoverOwner("new password"))
	assert.True(t, m.Owner(owner))
	_, err = m.Login("old password")
	assert.NoError(t, err)
	_, err = m.Login("new password")
	assert.ErrorIs(t, err, ErrCredentials)
}
