package screens

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestControlLifetime(t *testing.T) {
	for _, event := range []string{"expiry", "disconnect", "shutdown"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := New(time.Minute)
				defer m.Shutdown()
				screen, ticket, err := m.Advertise("a", "TV", time.Now())
				require.NoError(t, err)
				_, disconnect, err := m.ConnectReceiver(ticket, "a", time.Now())
				require.NoError(t, err)
				defer disconnect()
				session, err := m.Authorize(screen.ID, "a", time.Now())
				require.NoError(t, err)
				_, _, err = m.ControlContext(session.Token, "b", time.Now())
				require.ErrorIs(t, err, ErrForbidden)
				ctx, cancel, err := m.ControlContext(session.Token, "a", time.Now())
				require.NoError(t, err)
				defer cancel()
				require.NoError(t, ctx.Err())
				switch event {
				case "expiry":
					time.Sleep(time.Minute)
					synctest.Wait()
					require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
				case "disconnect":
					disconnect()
					require.ErrorIs(t, ctx.Err(), context.Canceled)
				case "shutdown":
					m.Shutdown()
					require.ErrorIs(t, ctx.Err(), context.Canceled)
				}
			})
		})
	}
}
