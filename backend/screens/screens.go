// Package screens owns runtime-only Flixr screen presence and control authority.
package screens

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrForbidden      = errors.New("screen authority is invalid")
	ErrExpired        = errors.New("screen authority expired")
	ErrUnavailable    = errors.New("screen is unavailable")
	ErrInvalidCommand = errors.New("invalid screen command")
)

type Screen struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	CatalogID  string `json:"catalog_id,omitempty"`
	PositionMS int64  `json:"position_ms,omitempty"`
}

type Session struct {
	Token     string    `json:"token"`
	ScreenID  string    `json:"screen_id"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Command struct {
	Type       string `json:"type"`
	CatalogID  string `json:"catalog_id,omitempty"`
	PositionMS int64  `json:"position_ms,omitempty"`
}
type presence struct {
	screen            Screen
	profileID, ticket string
	connected         bool
	commands          chan Command
}
type authority struct {
	screenID, profileID string
	expires             time.Time
}

type Manager struct {
	mu       sync.Mutex
	ttl      time.Duration
	screens  map[string]*presence
	tickets  map[string]authority
	sessions map[string]authority
	closed   bool
}

func New(ttl time.Duration) *Manager {
	return &Manager{ttl: ttl, screens: map[string]*presence{}, tickets: map[string]authority{}, sessions: map[string]authority{}}
}

func token() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (m *Manager) Advertise(profileID, name string, now time.Time) (Screen, string, error) {
	if profileID == "" || strings.TrimSpace(name) == "" {
		return Screen{}, "", ErrForbidden
	}
	id, err := token()
	if err != nil {
		return Screen{}, "", err
	}
	ticket, err := token()
	if err != nil {
		return Screen{}, "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Screen{}, "", ErrUnavailable
	}
	p := &presence{screen: Screen{ID: id, Name: strings.TrimSpace(name), State: "available"}, profileID: profileID, ticket: ticket, commands: make(chan Command, 8)}
	m.screens[id] = p
	m.tickets[ticket] = authority{screenID: id, profileID: profileID, expires: now.Add(m.ttl)}
	return p.screen, ticket, nil
}

func (m *Manager) ConnectReceiver(ticket, profileID string, now time.Time) (<-chan Command, func(), error) {
	m.mu.Lock()
	a, ok := m.tickets[ticket]
	p := m.screens[a.screenID]
	if !ok || p == nil || a.profileID != profileID || !now.Before(a.expires) || m.closed {
		delete(m.tickets, ticket)
		m.mu.Unlock()
		return nil, nil, ErrForbidden
	}
	delete(m.tickets, ticket)
	p.connected = true
	p.screen.State = "available"
	m.mu.Unlock()
	var once sync.Once
	disconnect := func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if current := m.screens[a.screenID]; current == p {
				delete(m.screens, a.screenID)
				for key, authority := range m.sessions {
					if authority.screenID == a.screenID {
						delete(m.sessions, key)
					}
				}
				close(p.commands)
			}
		})
	}
	return p.commands, disconnect, nil
}

func (m *Manager) List(profileID string) []Screen {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Screen{}
	for _, p := range m.screens {
		if p.profileID == profileID && p.connected {
			out = append(out, p.screen)
		}
	}
	return out
}
func (m *Manager) ListAll() []Screen {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Screen{}
	for _, p := range m.screens {
		if p.connected {
			out = append(out, p.screen)
		}
	}
	return out
}

func (m *Manager) Authorize(screenID, profileID string, now time.Time) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.screens[screenID]
	if p == nil || !p.connected {
		return Session{}, ErrUnavailable
	}
	if p.profileID != profileID {
		return Session{}, ErrForbidden
	}
	value, err := token()
	if err != nil {
		return Session{}, err
	}
	expires := now.Add(m.ttl)
	m.sessions[value] = authority{screenID: screenID, profileID: profileID, expires: expires}
	return Session{Token: value, ScreenID: screenID, ExpiresAt: expires}, nil
}

func (m *Manager) Validate(value, profileID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.sessions[value]
	if !ok || a.profileID != profileID {
		return ErrForbidden
	}
	if !now.Before(a.expires) {
		delete(m.sessions, value)
		return ErrExpired
	}
	if p := m.screens[a.screenID]; p == nil || !p.connected {
		return ErrUnavailable
	}
	return nil
}

func (m *Manager) Control(value, profileID string, command Command, now time.Time) (Screen, error) {
	if command.PositionMS < 0 || (command.Type != "play" && command.Type != "pause" && command.Type != "seek" && command.Type != "handoff") || (command.Type == "play" && command.CatalogID == "") {
		return Screen{}, ErrInvalidCommand
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.sessions[value]
	if !ok || a.profileID != profileID {
		return Screen{}, ErrForbidden
	}
	if !now.Before(a.expires) {
		delete(m.sessions, value)
		return Screen{}, ErrExpired
	}
	p := m.screens[a.screenID]
	if p == nil || !p.connected {
		return Screen{}, ErrUnavailable
	}
	switch command.Type {
	case "play":
		p.screen.State = "playing"
		p.screen.CatalogID = command.CatalogID
		p.screen.PositionMS = command.PositionMS
	case "pause":
		p.screen.State = "paused"
	case "seek":
		p.screen.PositionMS = command.PositionMS
	case "handoff":
		p.screen.State = "available"
		p.screen.CatalogID = ""
		p.screen.PositionMS = 0
		delete(m.sessions, value)
	}
	select {
	case p.commands <- command:
		return p.screen, nil
	default:
		return Screen{}, ErrUnavailable
	}
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, p := range m.screens {
		close(p.commands)
	}
	m.screens = map[string]*presence{}
	m.sessions = map[string]authority{}
	m.tickets = map[string]authority{}
}
