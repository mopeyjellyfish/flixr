// Package household owns local owner, profiles, sessions, and viewing progress.
package household

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"database/sql"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"golang.org/x/crypto/argon2"
)

var (
	ErrClaimed         = errors.New("owner already claimed")
	ErrToken           = errors.New("invalid setup token")
	ErrPIN             = errors.New("invalid pin")
	ErrProfileNotFound = errors.New("profile not found")
	ErrRateLimited     = errors.New("pin attempts rate limited")
	ErrHashSaturated   = errors.New("credential hashing saturated")
	ErrCredentials     = errors.New("invalid credentials")
	ErrRecovery        = errors.New("owner recovery unavailable")
)

type Profile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
}
type Manager struct {
	mu               sync.Mutex
	db               *sqlite.DB
	token            string
	ownerHash, salt  []byte
	ownerAttempts    int
	ownerLockedUntil time.Time
	profiles         map[string]profile
	sessions         map[string]string
	// hashGate bounds memory-hard Argon2 work and rejects excess requests instead of queuing them.
	hashGate chan struct{}
}
type profile struct {
	Profile
	hash, salt  []byte
	attempts    int
	lockedUntil time.Time
}

func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func New() (*Manager, error) {
	token, err := random()
	if err != nil {
		return nil, fmt.Errorf("generate setup token: %w", err)
	}
	return &Manager{token: token, profiles: map[string]profile{}, sessions: map[string]string{}, hashGate: make(chan struct{}, 2)}, nil
}
func Open(db *sqlite.DB) (*Manager, error) {
	m, err := New()
	if err != nil {
		return nil, err
	}
	m.db = db
	if _, err := db.Exec("DELETE FROM sessions WHERE revoked=1 OR expires_at<=?", time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("clean sessions: %w", err)
	}
	var hash, salt []byte
	err = db.QueryRow("SELECT password_hash, salt FROM owner WHERE id=1").Scan(&hash, &salt)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("load owner: %w", err)
	}
	if err == nil {
		m.ownerHash, m.salt, m.token = hash, salt, ""
	}
	rows, err := db.Query("SELECT id,name,pin_hash,salt,attempts,locked_until FROM profiles")
	if err != nil {
		return nil, fmt.Errorf("load profiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p profile
		var locked int64
		if err := rows.Scan(&p.ID, &p.Name, &p.hash, &p.salt, &p.attempts, &locked); err != nil {
			return nil, err
		}
		p.Protected = len(p.hash) > 0
		p.lockedUntil = time.Unix(locked, 0)
		m.profiles[p.ID] = p
	}
	return m, rows.Err()
}
func (m *Manager) SetupToken() string { m.mu.Lock(); defer m.mu.Unlock(); return m.token }
func (m *Manager) Claimed() bool      { m.mu.Lock(); defer m.mu.Unlock(); return len(m.ownerHash) > 0 }
func hash(secret string, salt []byte) []byte {
	return argon2.IDKey([]byte(secret), salt, 2, 64*1024, 2, 32)
}
func (m *Manager) derive(secret string, salt []byte) ([]byte, error) {
	select {
	case m.hashGate <- struct{}{}:
		defer func() { <-m.hashGate }()
		return hash(secret, salt), nil
	default:
		return nil, ErrHashSaturated
	}
}
func salt() ([]byte, error) { b := make([]byte, 16); _, err := rand.Read(b); return b, err }
func (m *Manager) Claim(token, password string) (string, error) {
	m.mu.Lock()
	claimed, expected := len(m.ownerHash) > 0, m.token
	m.mu.Unlock()
	if claimed {
		return "", ErrClaimed
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
		return "", ErrToken
	}
	if password == "" {
		return "", ErrCredentials
	}
	s, err := salt()
	if err != nil {
		return "", err
	}
	h, err := m.derive(password, s)
	if err != nil {
		return "", err
	}
	if m.db != nil {
		if _, err = m.db.Exec("INSERT INTO owner(id,password_hash,salt) VALUES(1,?,?)", h, s); err != nil {
			return "", fmt.Errorf("save owner: %w", err)
		}
	}
	m.mu.Lock()
	m.ownerHash, m.salt, m.token = h, s, ""
	m.mu.Unlock()
	return m.issue("owner")
}
func (m *Manager) Login(password string) (string, error) {
	m.mu.Lock()
	hashValue, saltValue, locked := append([]byte(nil), m.ownerHash...), append([]byte(nil), m.salt...), m.ownerLockedUntil
	m.mu.Unlock()
	if time.Now().Before(locked) {
		return "", ErrRateLimited
	}
	derived, err := m.derive(password, saltValue)
	if err != nil {
		return "", err
	}
	valid := len(hashValue) > 0 && subtle.ConstantTimeCompare(derived, hashValue) == 1
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Now().Before(m.ownerLockedUntil) {
		return "", ErrRateLimited
	}
	if !valid {
		m.ownerAttempts++
		if m.ownerAttempts >= 5 {
			m.ownerAttempts = 0
			m.ownerLockedUntil = time.Now().Add(time.Minute)
		}
		return "", ErrCredentials
	}
	m.ownerAttempts = 0
	return m.issueLocked("owner")
}

// RecoverOwner replaces the claimed owner's password from host-local recovery.
// It is intentionally unavailable without durable storage: a recovery must
// survive process interruption and revoke persisted owner sessions atomically.
func (m *Manager) RecoverOwner(password string) error {
	if password == "" || m.db == nil {
		return ErrRecovery
	}
	m.mu.Lock()
	claimed := len(m.ownerHash) > 0
	m.mu.Unlock()
	if !claimed {
		return ErrRecovery
	}
	s, err := salt()
	if err != nil {
		return fmt.Errorf("generate owner recovery salt: %w", err)
	}
	h, err := m.derive(password, s)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin owner recovery: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE owner SET password_hash=?,salt=? WHERE id=1", h, s)
	if err != nil {
		return fmt.Errorf("replace owner credential: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("replace owner credential: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("replace owner credential: %w", ErrRecovery)
	}
	if _, err := tx.Exec("UPDATE sessions SET revoked=1 WHERE subject='owner' AND revoked=0"); err != nil {
		return fmt.Errorf("revoke owner sessions: %w", err)
	}
	if _, err := tx.Exec("INSERT INTO audit_events(action,recorded_at) VALUES('owner_recovered',?)", time.Now().Unix()); err != nil {
		return fmt.Errorf("audit owner recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit owner recovery: %w", err)
	}
	m.mu.Lock()
	m.ownerHash, m.salt = h, s
	m.ownerAttempts, m.ownerLockedUntil = 0, time.Time{}
	m.mu.Unlock()
	return nil
}
func tokenHash(token string) []byte { s := sha256.Sum256([]byte(token)); return s[:] }
func (m *Manager) issue(subject string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.issueLocked(subject)
}
func (m *Manager) issueLocked(subject string) (string, error) {
	t, err := random()
	if err != nil {
		return "", err
	}
	if m.db != nil {
		if _, err = m.db.Exec("INSERT INTO sessions(token_hash,subject,expires_at) VALUES(?,?,?)", tokenHash(t), subject, time.Now().Add(24*time.Hour).Unix()); err != nil {
			return "", fmt.Errorf("save session: %w", err)
		}
	}
	if m.db == nil {
		m.sessions[t] = subject
	}
	return t, nil
}
func (m *Manager) subject(token string) string {
	if token == "" {
		return ""
	}
	if m.db != nil {
		var subject string
		if err := m.db.QueryRow("SELECT subject FROM sessions WHERE token_hash=? AND revoked=0 AND expires_at>?", tokenHash(token), time.Now().Unix()).Scan(&subject); err != nil {
			return ""
		}
		return subject
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[token]
}
func (m *Manager) Owner(session string) bool { return m.subject(session) == "owner" }
func (m *Manager) Logout(session string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		delete(m.sessions, session)
	}
	if m.db != nil {
		_, err := m.db.Exec("UPDATE sessions SET revoked=1 WHERE token_hash=?", tokenHash(session))
		return err
	}
	return nil
}
func (m *Manager) CreateProfile(name, pin string) (Profile, error) {
	id, err := random()
	if err != nil {
		return Profile{}, err
	}
	p := profile{Profile: Profile{ID: id, Name: name, Protected: pin != ""}}
	if pin != "" {
		p.salt, err = salt()
		if err != nil {
			return Profile{}, err
		}
		p.hash, err = m.derive(pin, p.salt)
		if err != nil {
			return Profile{}, err
		}
	}
	if m.db != nil {
		if _, err = m.db.Exec("INSERT INTO profiles(id,name,pin_hash,salt) VALUES(?,?,?,?)", p.ID, p.Name, p.hash, p.salt); err != nil {
			return Profile{}, fmt.Errorf("save profile: %w", err)
		}
	}
	m.mu.Lock()
	m.profiles[p.ID] = p
	m.mu.Unlock()
	return p.Profile, nil
}
func (m *Manager) Profiles() []Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Profile, 0, len(m.profiles))
	for _, p := range m.profiles {
		out = append(out, p.Profile)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (m *Manager) UpdateProfile(id, name, pin string, unprotect bool) (Profile, error) {
	m.mu.Lock()
	p, ok := m.profiles[id]
	m.mu.Unlock()
	if !ok {
		return Profile{}, ErrProfileNotFound
	}
	if name != "" {
		p.Name = name
	}
	if unprotect {
		p.hash = nil
		p.salt = nil
		p.Protected = false
	} else if pin != "" {
		var err error
		p.salt, err = salt()
		if err != nil {
			return Profile{}, err
		}
		p.hash, err = m.derive(pin, p.salt)
		if err != nil {
			return Profile{}, err
		}
		p.Protected = true
	}
	if m.db != nil {
		result, err := m.db.Exec("UPDATE profiles SET name=?,pin_hash=?,salt=? WHERE id=?", p.Name, p.hash, p.salt, id)
		if err != nil {
			return Profile{}, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return Profile{}, err
		}
		if updated == 0 {
			return Profile{}, ErrProfileNotFound
		}
	}
	m.mu.Lock()
	m.profiles[id] = p
	m.mu.Unlock()
	return p.Profile, nil
}
func (m *Manager) Select(id, pin string) (string, error) {
	m.mu.Lock()
	p, ok := m.profiles[id]
	m.mu.Unlock()
	if !ok {
		return "", ErrPIN
	}
	now := time.Now()
	if now.Before(p.lockedUntil) {
		return "", ErrRateLimited
	}
	valid := !p.Protected
	if p.Protected {
		derived, err := m.derive(pin, p.salt)
		if err != nil {
			return "", err
		}
		valid = subtle.ConstantTimeCompare(derived, p.hash) == 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok = m.profiles[id]
	if !ok {
		return "", ErrPIN
	}
	now = time.Now()
	if now.Before(p.lockedUntil) {
		return "", ErrRateLimited
	}
	if !valid {
		p.attempts++
		if p.attempts >= 5 {
			p.attempts = 0
			p.lockedUntil = now.Add(time.Minute)
		}
		if err := m.persistAttempt(p); err != nil {
			return "", fmt.Errorf("persist pin rate limit: %w", err)
		}
		m.profiles[id] = p
		return "", ErrPIN
	}
	p.attempts = 0
	if err := m.persistAttempt(p); err != nil {
		return "", fmt.Errorf("persist pin rate limit: %w", err)
	}
	m.profiles[id] = p
	return m.issueLocked(id)
}
func (m *Manager) persistAttempt(p profile) error {
	if m.db == nil {
		return nil
	}
	_, err := m.db.Exec("UPDATE profiles SET attempts=?,locked_until=? WHERE id=?", p.attempts, p.lockedUntil.Unix(), p.ID)
	return err
}
func (m *Manager) Profile(session string) (Profile, bool) {
	id := m.subject(session)
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[id]
	return p.Profile, ok
}
func (m *Manager) Progress(session, catalogID string, position int64) error {
	return m.ProgressForProfile(m.subject(session), catalogID, position)
}

// SetWatched applies one explicit profile action atomically across its items.
func (m *Manager) SetWatched(profileID string, catalogIDs []string, watched bool) error {
	if profileID == "" || len(catalogIDs) == 0 || m.db == nil {
		return ErrCredentials
	}
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin watched update: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	for _, id := range catalogIDs {
		if id == "" {
			return ErrCredentials
		}
		position, completed := int64(0), 0
		if watched {
			position, completed = 1<<62, 1
		}
		if _, err := tx.Exec(`INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation) VALUES(?,?,?,?,?,?,1) ON CONFLICT(profile_id,catalog_id) DO UPDATE SET position_ms=excluded.position_ms,updated_at=excluded.updated_at,completed=excluded.completed,completed_at=excluded.completed_at,generation=progress.generation+1,observation=0`, profileID, id, position, now, completed, map[bool]int64{true: now, false: 0}[watched]); err != nil {
			return fmt.Errorf("save watched state: %w", err)
		}
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func (m *Manager) Position(session, catalogID string) (int64, error) {
	position, _, err := m.ProgressState(session, catalogID)
	return position, err
}

// ProgressState reads position and its legacy write token in one snapshot.
func (m *Manager) ProgressState(session, catalogID string) (int64, int64, error) {
	id := m.subject(session)
	if id == "" || id == "owner" {
		return 0, 0, ErrCredentials
	}
	if m.db == nil {
		return 0, 0, nil
	}
	var position, generation int64
	err := m.db.QueryRow(`SELECT CASE WHEN completed=1 THEN 0 ELSE position_ms END,generation FROM progress WHERE profile_id=? AND catalog_id=?`, id, catalogID).Scan(&position, &generation)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return position, generation, err
}
