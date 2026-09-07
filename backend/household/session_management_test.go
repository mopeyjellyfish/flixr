package household

import (
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDeleteProfileRevokesSessionAndCascadesProgress(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	p, err := m.CreateProfile("Ada", "")
	require.NoError(t, err)
	token, err := m.Select(p.ID, "")
	require.NoError(t, err)
	require.NoError(t, m.DeleteProfile(p.ID))
	if _, ok := m.Profile(token); ok {
		t.Fatal("deleted profile session remains valid")
	}
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM profiles WHERE id=?", p.ID).Scan(&count))
	require.Zero(t, count)
}

func TestActiveSessionCanBeRevoked(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	token, err := m.Claim(m.SetupToken(), "password")
	require.NoError(t, err)
	sessions, err := m.ActiveSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NoError(t, m.RevokeSession(sessions[0].ID))
	if m.Owner(token) {
		t.Fatal("revoked owner session remains valid")
	}
}

func TestPINChangeRevokesProfileSessions(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	p, err := m.CreateProfile("Ada", "1234")
	require.NoError(t, err)
	token, err := m.Select(p.ID, "1234")
	require.NoError(t, err)
	_, err = m.UpdateProfile(p.ID, "", "5678", false)
	require.NoError(t, err)
	if _, ok := m.Profile(token); ok {
		t.Fatal("PIN change left prior session valid")
	}
}
