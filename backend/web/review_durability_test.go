package web_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateProfileWriteFailureReturnsInternalError(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	house, err := household.Open(db)
	require.NoError(t, err)
	session, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	require.NoError(t, err)
	profile, err := house.CreateProfile("Ada", "")
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER fail_profile_update BEFORE UPDATE ON profiles BEGIN SELECT RAISE(FAIL, 'write failed'); END`)
	require.NoError(t, err)
	defer db.Exec("DROP TRIGGER fail_profile_update")

	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	require.NoError(t, err)
	handler := web.NewServer(house, c).Handler()
	request := httptest.NewRequest(http.MethodPatch, "http://flixr.test/api/v1/profiles/"+profile.ID, bytes.NewBufferString(`{"name":"Grace"}`))
	request.Header.Set("Origin", "http://flixr.test")
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.JSONEq(t, `{"error":{"code":"profile_failed"}}`, response.Body.String())
}
