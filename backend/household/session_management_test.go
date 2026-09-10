package household

import (
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestOldPINCannotCompleteAfterRotation(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	p, err := m.CreateProfile("Ada", "1111")
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m.deriveFn = func(secret string, salt []byte) ([]byte, error) {
		if secret == "1111" {
			once.Do(func() { close(started); <-release })
		}
		return hash(secret, salt), nil
	}
	result := make(chan error, 1)
	go func() { _, err := m.Select(p.ID, "1111"); result <- err }()
	<-started
	_, err = m.UpdateProfile(p.ID, "", "2222", false)
	require.NoError(t, err)
	close(release)
	require.ErrorIs(t, <-result, ErrPIN)
	_, err = m.Select(p.ID, "2222")
	require.NoError(t, err)
}

func TestDeleteProfileRevokesSessionAndCascadesProgress(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	p, err := m.CreateProfile("Ada", "")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','film.mp4')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO progress(profile_id,catalog_id,position_ms) VALUES(?,?,1)", p.ID, "film")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO profile_film_list(profile_id,catalog_id,added_at) VALUES(?,?,1)", p.ID, "film")
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
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM progress WHERE profile_id=?", p.ID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM profile_film_list WHERE profile_id=?", p.ID).Scan(&count))
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

func TestSessionIdentityIsStableAndDoesNotExposeTheBearerToken(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	token, err := m.Claim(m.SetupToken(), "password")
	require.NoError(t, err)

	identity, ok := m.SessionIdentity(token)
	require.True(t, ok)
	require.NotEqual(t, token, identity)
	sessions, err := m.ActiveSessions()
	require.NoError(t, err)
	require.Equal(t, sessions[0].ID, identity)

	require.NoError(t, m.Logout(token))
	_, ok = m.SessionIdentity(token)
	require.False(t, ok)
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

func TestDeleteProfileFailureKeepsProfileAndSession(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	m, err := Open(db)
	require.NoError(t, err)
	p, err := m.CreateProfile("Ada", "")
	require.NoError(t, err)
	token, err := m.Select(p.ID, "")
	require.NoError(t, err)
	_, err = db.Exec("CREATE TRIGGER fail_profile_delete BEFORE DELETE ON profiles BEGIN SELECT RAISE(ABORT, 'interrupted'); END")
	require.NoError(t, err)
	require.Error(t, m.DeleteProfile(p.ID))
	if _, ok := m.Profile(token); !ok {
		t.Fatal("failed deletion revoked session")
	}
}

func TestDeletedProfileStaysGoneAfterRestartWithoutAffectingAnotherHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	m, err := Open(db)
	require.NoError(t, err)
	one, _ := m.CreateProfile("One", "")
	two, _ := m.CreateProfile("Two", "")
	_, err = db.Exec("INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','film.mp4')")
	require.NoError(t, err)
	require.NoError(t, m.ProgressForProfile(two.ID, "film", 42))
	require.NoError(t, m.DeleteProfile(one.ID))
	require.NoError(t, db.Close())
	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	m, err = Open(db)
	require.NoError(t, err)
	if len(m.Profiles()) != 1 || m.Profiles()[0].ID != two.ID {
		t.Fatal("profile deletion did not persist")
	}
	token, err := m.Select(two.ID, "")
	require.NoError(t, err)
	position, err := m.Position(token, "film")
	require.NoError(t, err)
	require.EqualValues(t, 42, position)
}
