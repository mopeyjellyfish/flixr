package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/mopeyjellyfish/flixr/backend/screens"
)

type screenMessage struct {
	Version    int    `json:"version"`
	Type       string `json:"type"`
	CatalogID  string `json:"catalog_id,omitempty"`
	PositionMS int64  `json:"position_ms"`
}

func (s *Server) screenProfile(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.profile(w, r) {
		return "", false
	}
	profile, ok := s.house.Profile(s.session(r))
	return profile.ID, ok
}

func (s *Server) advertiseScreen(w http.ResponseWriter, r *http.Request) {
	profileID, ok := s.screenProfile(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	screen, ticket, err := s.screens.Advertise(profileID, body.Name, time.Now())
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_screen")
		return
	}
	write(w, http.StatusCreated, map[string]any{"screen": screen, "ticket": ticket})
}

func (s *Server) listScreens(w http.ResponseWriter, r *http.Request) {
	profileID, ok := s.screenProfile(w, r)
	if !ok {
		return
	}
	write(w, http.StatusOK, map[string]any{"screens": s.screens.List(profileID)})
}
func (s *Server) ownerScreens(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	write(w, http.StatusOK, map[string]any{"screens": s.screens.ListAll()})
}
func (s *Server) authorizeScreen(w http.ResponseWriter, r *http.Request) {
	profileID, ok := s.screenProfile(w, r)
	if !ok {
		return
	}
	session, err := s.screens.Authorize(r.PathValue("id"), profileID, time.Now())
	if err != nil {
		screenFailure(w, err)
		return
	}
	write(w, http.StatusCreated, session)
}

func (s *Server) websocketOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != scheme(r)+r.Host {
		fail(w, http.StatusForbidden, "bad_origin")
		return false
	}
	return true
}

func (s *Server) screenReceiver(w http.ResponseWriter, r *http.Request) {
	if !s.websocketOrigin(w, r) {
		return
	}
	profileID, ok := s.screenProfile(w, r)
	if !ok {
		return
	}
	commands, disconnect, err := s.screens.ConnectReceiver(r.URL.Query().Get("ticket"), profileID, time.Now())
	if err != nil {
		screenFailure(w, err)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		disconnect()
		return
	}
	defer conn.CloseNow()
	defer disconnect()
	closed := conn.CloseRead(s.screens.Context())
	for {
		select {
		case <-closed.Done():
			return
		case command, open := <-commands:
			if !open {
				return
			}
			current, selected := s.house.Profile(s.session(r))
			if !selected || current.ID != profileID {
				return
			}
			message := screenMessage{Version: 1, Type: command.Type, CatalogID: command.CatalogID, PositionMS: command.PositionMS}
			data, _ := json.Marshal(message)
			writeCtx, cancel := context.WithTimeout(closed, 5*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) screenControl(w http.ResponseWriter, r *http.Request) {
	if !s.websocketOrigin(w, r) {
		return
	}
	profileID, ok := s.screenProfile(w, r)
	if !ok {
		return
	}
	authority := r.URL.Query().Get("token")
	ctx, cancel, err := s.screens.ControlContext(authority, profileID, time.Now())
	if err != nil {
		screenFailure(w, err)
		return
	}
	defer cancel()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		current, selected := s.house.Profile(s.session(r))
		if !selected || current.ID != profileID {
			return
		}
		var message screenMessage
		if json.Unmarshal(data, &message) != nil || message.Version != 1 {
			_ = conn.Close(websocket.StatusPolicyViolation, "unsupported protocol")
			return
		}
		_, err = s.screens.Control(authority, profileID, screens.Command{Type: message.Type, CatalogID: message.CatalogID, PositionMS: message.PositionMS}, time.Now())
		if err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "screen authority ended")
			return
		}
	}
}

func screenFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, screens.ErrExpired):
		fail(w, http.StatusForbidden, "screen_session_expired")
	case errors.Is(err, screens.ErrUnavailable):
		fail(w, http.StatusConflict, "screen_unavailable")
	case errors.Is(err, screens.ErrInvalidCommand):
		fail(w, http.StatusBadRequest, "screen_command_invalid")
	default:
		fail(w, http.StatusForbidden, "screen_session_invalid")
	}
}
