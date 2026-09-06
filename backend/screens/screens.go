// Package screens owns runtime-only Flixr screen presence and control authority.
package screens

import (
	"context"
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
	expires           time.Time
	connected         bool
	commands          chan Command
	ctx               context.Context
	cancel            context.CancelFunc
}
type authority struct {
	screenID, profileID string
	expires             time.Time
	claimed             bool
}

type Manager struct {
	mu       sync.Mutex
	ttl      time.Duration
	screens  map[string]*presence
	tickets  map[string]authority
	sessions map[string]authority
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
}

func New(ttl time.Duration) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{ttl: ttl, screens: map[string]*presence{}, tickets: map[string]authority{}, sessions: map[string]authority{}, ctx: ctx, cancel: cancel}
}

// Context is canceled when shutdown starts, including for upgraded connections.
func (m *Manager) Context() context.Context { return m.ctx }

// ControlContext bounds socket I/O by authority expiry and receiver lifetime.
// The caller must call cancel when the socket closes.
func (m *Manager) ControlContext(value, profileID string, now time.Time) (context.Context, context.CancelFunc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.sessions[value]
	if !ok || a.profileID != profileID || a.claimed || m.closed {
		return nil, nil, ErrForbidden
	}
	if !now.Before(a.expires) {
		return nil, nil, ErrExpired
	}
	p := m.screens[a.screenID]
	if p == nil || !p.connected {
		return nil, nil, ErrUnavailable
	}
	a.claimed = true
	m.sessions[value] = a
	ctx, cancel := context.WithDeadline(p.ctx, a.expires)
	return ctx, func() {
		cancel()
		m.mu.Lock()
		delete(m.sessions, value)
		m.mu.Unlock()
	}, nil
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
	m.pruneLocked(now)
	if len(m.screens) >= 128 || len(name) > 120 {
		return Screen{}, "", ErrUnavailable
	}
	p := &presence{screen: Screen{ID: id, Name: strings.TrimSpace(name), State: "available"}, profileID: profileID, ticket: ticket, expires: now.Add(m.ttl), commands: make(chan Command, 8)}
	p.ctx, p.cancel = context.WithCancel(m.ctx)
	m.screens[id] = p
	m.tickets[ticket] = authority{screenID: id, profileID: profileID, expires: p.expires}
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
				p.cancel()
			}
		})
	}
	return p.commands, disconnect, nil
}

func (m *Manager) List(profileID string, at ...time.Time) []Screen {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if len(at) != 0 {
		now = at[0]
	}
	m.pruneLocked(now)
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
	m.pruneLocked(time.Now())
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
	m.pruneLocked(now)
	p := m.screens[screenID]
	if p == nil || !p.connected {
		return Session{}, ErrUnavailable
	}
	if p.profileID != profileID {
		return Session{}, ErrForbidden
	}
	if len(m.sessions) >= 128 {
		return Session{}, ErrUnavailable
	}
	value, err := token()
	if err != nil {
		return Session{}, err
	}
	expires := now.Add(m.ttl)
	m.sessions[value] = authority{screenID: screenID, profileID: profileID, expires: expires}
	return Session{Token: value, ScreenID: screenID, ExpiresAt: expires}, nil
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
	updated := p.screen
	switch command.Type {
	case "play":
		updated.State = "playing"
		updated.CatalogID = command.CatalogID
		updated.PositionMS = command.PositionMS
	case "pause":
		updated.State = "paused"
	case "seek":
		updated.PositionMS = command.PositionMS
	case "handoff":
		updated.State = "available"
		updated.CatalogID = ""
		updated.PositionMS = 0
	}
	select {
	case p.commands <- command:
		p.screen = updated
		if command.Type == "handoff" {
			delete(m.sessions, value)
		}
		return updated, nil
	default:
		return Screen{}, ErrUnavailable
	}
}

func (m *Manager) pruneLocked(now time.Time) {
	for key, authority := range m.tickets {
		if !now.Before(authority.expires) {
			delete(m.tickets, key)
		}
	}
	for key, authority := range m.sessions {
		if !now.Before(authority.expires) {
			delete(m.sessions, key)
		}
	}
	for id, p := range m.screens {
		if !p.connected && !now.Before(p.expires) {
			delete(m.screens, id)
			p.cancel()
		}
	}
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	m.cancel()
	for _, p := range m.screens {
		close(p.commands)
		p.cancel()
	}
	m.screens = map[string]*presence{}
	m.sessions = map[string]authority{}
	m.tickets = map[string]authority{}
}
