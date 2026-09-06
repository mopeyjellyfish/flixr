package household

import (
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCleansInactiveSessionsAndKeepsDurableSessionsOutOfMemory(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	m, err := Open(db)
	require.NoError(t, err)
	owner, err := m.Claim(m.SetupToken(), "correct horse battery staple")
	require.NoError(t, err)
	assert.Empty(t, m.sessions)
	require.NoError(t, m.Logout(owner))
	_, err = db.Exec("INSERT INTO sessions(token_hash,subject,expires_at) VALUES(?,?,?)", []byte("expired"), "owner", time.Now().Add(-time.Hour).Unix())
	require.NoError(t, err)

	m, err = Open(db)
	require.NoError(t, err)
	assert.Empty(t, m.sessions)
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count))
	assert.Zero(t, count)
}

func TestUpdateProfileReturnsNotFound(t *testing.T) {
	m, err := New()
	require.NoError(t, err)
	_, err = m.UpdateProfile("missing", "Ada", "", false)
	assert.ErrorIs(t, err, ErrProfileNotFound)
}
