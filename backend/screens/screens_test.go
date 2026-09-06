package screens

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileScopedExpiringControl(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	manager := New(time.Minute)
	screen, ticket, err := manager.Advertise("profile-a", "Living room", now)
	require.NoError(t, err)
	require.NotEmpty(t, ticket)
	assert.Equal(t, "Living room", screen.Name)

	commands, disconnect, err := manager.ConnectReceiver(ticket, "profile-a", now)
	require.NoError(t, err)
	defer disconnect()
	session, err := manager.Authorize(screen.ID, "profile-a", now)
	require.NoError(t, err)

	_, err = manager.Control(session.Token, "profile-b", Command{Type: "pause"}, now)
	assert.ErrorIs(t, err, ErrForbidden)
	updated, err := manager.Control(session.Token, "profile-a", Command{Type: "play", CatalogID: "film-1", PositionMS: 1200}, now)
	require.NoError(t, err)
	assert.Equal(t, "playing", updated.State)
	assert.Equal(t, Command{Type: "play", CatalogID: "film-1", PositionMS: 1200}, <-commands)
	_, err = manager.Control(session.Token, "profile-a", Command{Type: "pause"}, now.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrExpired)

	disconnect()
	assert.Empty(t, manager.List("profile-a"))
}

func TestRestartInvalidatesAuthority(t *testing.T) {
	now := time.Now()
	first := New(time.Minute)
	screen, ticket, err := first.Advertise("profile", "Screen", now)
	require.NoError(t, err)
	_, _, err = first.ConnectReceiver(ticket, "profile", now)
	require.NoError(t, err)
	session, err := first.Authorize(screen.ID, "profile", now)
	require.NoError(t, err)

	second := New(time.Minute)
	_, err = second.Control(session.Token, "profile", Command{Type: "pause"}, now)
	assert.ErrorIs(t, err, ErrForbidden)
}
