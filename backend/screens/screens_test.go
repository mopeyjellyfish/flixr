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

func TestFailedQueueDoesNotPublishCommandState(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	manager := New(time.Minute)
	screen, ticket, err := manager.Advertise("profile", "Screen", now)
	require.NoError(t, err)
	_, disconnect, err := manager.ConnectReceiver(ticket, "profile", now)
	require.NoError(t, err)
	defer disconnect()
	session, err := manager.Authorize(screen.ID, "profile", now)
	require.NoError(t, err)

	for range 8 {
		_, err = manager.Control(session.Token, "profile", Command{Type: "pause"}, now)
		require.NoError(t, err)
	}
	_, err = manager.Control(session.Token, "profile", Command{Type: "play", CatalogID: "film-1", PositionMS: 4200}, now)
	assert.ErrorIs(t, err, ErrUnavailable)
	listed := manager.List("profile", now)
	require.Len(t, listed, 1)
	assert.Equal(t, "paused", listed[0].State)
	assert.Empty(t, listed[0].CatalogID)
}

func TestLivePresenceSurvivesTicketExpiry(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	manager := New(time.Minute)
	_, ticket, err := manager.Advertise("profile", "Screen", now)
	require.NoError(t, err)
	_, disconnect, err := manager.ConnectReceiver(ticket, "profile", now)
	require.NoError(t, err)
	defer disconnect()
	assert.Len(t, manager.List("profile", now), 1)
	assert.Len(t, manager.List("profile", now.Add(time.Minute)), 1)
}

func TestUnconnectedPresenceExpires(t *testing.T) {
	now := time.Now()
	manager := New(time.Minute)
	defer manager.Shutdown()
	_, _, err := manager.Advertise("profile", "Screen", now)
	require.NoError(t, err)
	manager.List("profile", now.Add(time.Minute))
	assert.Empty(t, manager.screens)
	assert.Empty(t, manager.tickets)
}
