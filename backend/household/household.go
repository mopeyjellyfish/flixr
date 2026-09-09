// Package household owns local owner, profiles, sessions, and viewing progress.
package household

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"database/sql"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"golang.org/x/crypto/argon2"
)

var (
	ErrClaimed                   = errors.New("owner already claimed")
	ErrToken                     = errors.New("invalid setup token")
	ErrPIN                       = errors.New("invalid pin")
	ErrProfileNotFound           = errors.New("profile not found")
	ErrRateLimited               = errors.New("pin attempts rate limited")
	ErrHashSaturated             = errors.New("credential hashing saturated")
	ErrCredentials               = errors.New("invalid credentials")
	ErrRecovery                  = errors.New("owner recovery unavailable")
	ErrSessionNotFound           = errors.New("session not found")
	ErrInvalidSubtitlePreference = errors.New("invalid subtitle preference")
	ErrInvalidPIN                = errors.New("pin must be 4 to 6 digits")
)

// validPIN reports whether a profile PIN is 4 to 6 ASCII digits. An empty PIN
// means "no PIN" and is handled by the callers.
func validPIN(pin string) bool {
	if len(pin) < 4 || len(pin) > 6 {
		return false
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type SubtitleMode string

const (
	SubtitleAutomatic SubtitleMode = "automatic"
	SubtitleOff       SubtitleMode = "off"
)

type SubtitlePreference struct {
	Mode      SubtitleMode `json:"mode"`
	Language  string       `json:"language,omitempty"`
	PreferSDH bool         `json:"prefer_sdh,omitempty"`
}

type Profile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
}
type Session struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	ExpiresAt int64  `json:"expires_at"`
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
	deriveFn func(string, []byte) ([]byte, error)
}
type profile struct {
	Profile
	hash, salt    []byte
	attempts      int
	lockedUntil   time.Time
	audioLanguage string
	subtitle      SubtitlePreference
	policy        access.Policy
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
	rows, err := db.Query("SELECT profiles.id,name,pin_hash,salt,attempts,locked_until,COALESCE(profile_audio_preferences.language,''),COALESCE(profile_subtitle_preferences.mode,'automatic'),COALESCE(profile_subtitle_preferences.language,''),COALESCE(profile_subtitle_preferences.prefer_sdh,0) FROM profiles LEFT JOIN profile_audio_preferences ON profile_audio_preferences.profile_id=profiles.id LEFT JOIN profile_subtitle_preferences ON profile_subtitle_preferences.profile_id=profiles.id")
	if err != nil {
		return nil, fmt.Errorf("load profiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p profile
		var locked int64
		var preferSDH int
		if err := rows.Scan(&p.ID, &p.Name, &p.hash, &p.salt, &p.attempts, &locked, &p.audioLanguage, &p.subtitle.Mode, &p.subtitle.Language, &preferSDH); err != nil {
			return nil, err
		}
		p.subtitle.PreferSDH = preferSDH != 0
		p.Protected = len(p.hash) > 0
		p.lockedUntil = time.Unix(locked, 0)
		if m.db != nil {
			var libraries, region, rating, unrated, allowTags, denyTags string
			var version int64
			if err := m.db.QueryRow(`SELECT library_ids_json,rating_region,max_rating,unrated_policy,allow_tags_json,deny_tags_json,version FROM profile_access_policies WHERE profile_id=?`, p.ID).Scan(&libraries, &region, &rating, &unrated, &allowTags, &denyTags, &version); err != nil {
				return nil, fmt.Errorf("load profile access policy: %w", err)
			}
			p.policy, err = scanPolicy(libraries, region, rating, unrated, allowTags, denyTags, version)
			if err != nil {
				return nil, err
			}
		}
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
	if m.deriveFn != nil {
		return m.deriveFn(secret, salt)
	}
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

// SessionIdentity returns a non-secret identifier for an active bearer session.
func (m *Manager) SessionIdentity(session string) (string, bool) {
	if m.subject(session) == "" {
		return "", false
	}
	return base64.RawURLEncoding.EncodeToString(tokenHash(session)), true
}

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
func (m *Manager) ActiveSessions() ([]Session, error) {
	if m.db == nil {
		return nil, nil
	}
	rows, err := m.db.Query("SELECT token_hash,subject,expires_at FROM sessions WHERE revoked=0 AND expires_at>? ORDER BY expires_at DESC", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var hash []byte
		var s Session
		if err := rows.Scan(&hash, &s.Subject, &s.ExpiresAt); err != nil {
			return nil, err
		}
		s.ID = base64.RawURLEncoding.EncodeToString(hash)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
func (m *Manager) RevokeSession(id string) error {
	hash, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(hash) != sha256.Size {
		return ErrSessionNotFound
	}
	if m.db == nil {
		return ErrSessionNotFound
	}
	result, err := m.db.Exec("UPDATE sessions SET revoked=1 WHERE token_hash=? AND revoked=0", hash)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return ErrSessionNotFound
	}
	return nil
}
func (m *Manager) DeleteProfile(id string) error {
	_, err := m.DeleteProfileAndRevokeSessions(id)
	return err
}

// DeleteProfileAndRevokeSessions returns the non-secret identities revoked by
// the committed deletion so runtime resources can be released afterward.
func (m *Manager) DeleteProfileAndRevokeSessions(id string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.profiles[id]
	if !ok {
		return nil, ErrProfileNotFound
	}
	var revoked []string
	if m.db != nil {
		tx, err := m.db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		revoked, err = revokeSubjectSessions(tx, id)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("DELETE FROM profiles WHERE id=?", id); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	} else {
		revoked = m.revokeMemorySessionsLocked(id)
	}
	delete(m.profiles, id)
	return revoked, nil
}
func (m *Manager) CreateProfile(name, pin string) (Profile, error) {
	if pin != "" && !validPIN(pin) {
		return Profile{}, ErrInvalidPIN
	}
	id, err := random()
	if err != nil {
		return Profile{}, err
	}
	policy := access.Unrestricted()
	policy.Version = 1
	p := profile{Profile: Profile{ID: id, Name: name, Protected: pin != ""}, policy: policy}
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
	profile, _, err := m.UpdateProfileAndRevokeSessions(id, name, pin, unprotect)
	return profile, err
}

// UpdateProfileAndRevokeSessions reports bearer identities revoked by a
// committed credential change. Name-only edits return no identities.
func (m *Manager) UpdateProfileAndRevokeSessions(id, name, pin string, unprotect bool) (Profile, []string, error) {
	if !unprotect && pin != "" && !validPIN(pin) {
		return Profile{}, nil, ErrInvalidPIN
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[id]
	if !ok {
		return Profile{}, nil, ErrProfileNotFound
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
			return Profile{}, nil, err
		}
		p.hash, err = m.derive(pin, p.salt)
		if err != nil {
			return Profile{}, nil, err
		}
		p.Protected = true
	}
	var revoked []string
	if m.db != nil {
		tx, err := m.db.Begin()
		if err != nil {
			return Profile{}, nil, err
		}
		defer tx.Rollback()
		result, err := tx.Exec("UPDATE profiles SET name=?,pin_hash=?,salt=? WHERE id=?", p.Name, p.hash, p.salt, id)
		if err != nil {
			return Profile{}, nil, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return Profile{}, nil, err
		}
		if updated == 0 {
			return Profile{}, nil, ErrProfileNotFound
		}
		if pin != "" || unprotect {
			revoked, err = revokeSubjectSessions(tx, id)
			if err != nil {
				return Profile{}, nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return Profile{}, nil, err
		}
	} else if pin != "" || unprotect {
		revoked = m.revokeMemorySessionsLocked(id)
	}
	m.profiles[id] = p
	return p.Profile, revoked, nil
}

func revokeSubjectSessions(tx *sql.Tx, subject string) ([]string, error) {
	rows, err := tx.Query("SELECT token_hash FROM sessions WHERE subject=? AND revoked=0", subject)
	if err != nil {
		return nil, err
	}
	var identities []string
	for rows.Next() {
		var hash []byte
		if err := rows.Scan(&hash); err != nil {
			rows.Close()
			return nil, err
		}
		identities = append(identities, base64.RawURLEncoding.EncodeToString(hash))
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec("UPDATE sessions SET revoked=1 WHERE subject=? AND revoked=0", subject); err != nil {
		return nil, err
	}
	return identities, nil
}

func (m *Manager) revokeMemorySessionsLocked(subject string) []string {
	var identities []string
	for token, sessionSubject := range m.sessions {
		if sessionSubject != subject {
			continue
		}
		identities = append(identities, base64.RawURLEncoding.EncodeToString(tokenHash(token)))
		delete(m.sessions, token)
	}
	return identities
}
func (m *Manager) Select(id, pin string) (string, error) {
	for {
		m.mu.Lock()
		p, ok := m.profiles[id]
		if !ok {
			m.mu.Unlock()
			return "", ErrPIN
		}
		if time.Now().Before(p.lockedUntil) {
			m.mu.Unlock()
			return "", ErrRateLimited
		}
		hashValue, saltValue := append([]byte(nil), p.hash...), append([]byte(nil), p.salt...)
		m.mu.Unlock()
		valid := !p.Protected
		if p.Protected {
			derived, err := m.derive(pin, saltValue)
			if err != nil {
				return "", err
			}
			valid = subtle.ConstantTimeCompare(derived, hashValue) == 1
		}
		m.mu.Lock()
		current, ok := m.profiles[id]
		if !ok {
			m.mu.Unlock()
			return "", ErrPIN
		}
		if !bytes.Equal(current.hash, hashValue) || !bytes.Equal(current.salt, saltValue) {
			m.mu.Unlock()
			continue
		}
		p = current
		now := time.Now()
		if now.Before(p.lockedUntil) {
			m.mu.Unlock()
			return "", ErrRateLimited
		}
		if !valid {
			p.attempts++
			if p.attempts >= 5 {
				p.attempts = 0
				p.lockedUntil = now.Add(time.Minute)
			}
			if err := m.persistAttempt(p); err != nil {
				m.mu.Unlock()
				return "", fmt.Errorf("persist pin rate limit: %w", err)
			}
			m.profiles[id] = p
			m.mu.Unlock()
			return "", ErrPIN
		}
		p.attempts = 0
		if err := m.persistAttempt(p); err != nil {
			m.mu.Unlock()
			return "", fmt.Errorf("persist pin rate limit: %w", err)
		}
		m.profiles[id] = p
		token, err := m.issueLocked(id)
		m.mu.Unlock()
		return token, err
	}
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

// AudioLanguage returns the normalized language preference for one profile.
func (m *Manager) AudioLanguage(profileID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[profileID]
	if !ok {
		return "", ErrProfileNotFound
	}
	return p.audioLanguage, nil
}

// SaveAudioLanguage remembers a validated catalog track's language.
func (m *Manager) SaveAudioLanguage(profileID, language string) error {
	language = strings.ToLower(strings.TrimSpace(language))
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[profileID]
	if !ok {
		return ErrProfileNotFound
	}
	if m.db != nil {
		if _, err := m.db.Exec("INSERT INTO profile_audio_preferences(profile_id,language) VALUES(?,?) ON CONFLICT(profile_id) DO UPDATE SET language=excluded.language", profileID, language); err != nil {
			return fmt.Errorf("save audio language: %w", err)
		}
	}
	p.audioLanguage = language
	m.profiles[profileID] = p
	return nil
}

// SubtitlePreference returns the durable selection policy for one profile.
func (m *Manager) SubtitlePreference(profileID string) (SubtitlePreference, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[profileID]
	if !ok {
		return SubtitlePreference{}, ErrProfileNotFound
	}
	if p.subtitle.Mode == "" {
		p.subtitle.Mode = SubtitleAutomatic
	}
	return p.subtitle, nil
}

// SaveSubtitlePreference persists an explicit off or automatic language policy.
func (m *Manager) SaveSubtitlePreference(profileID string, preference SubtitlePreference) error {
	preference.Language = strings.ToLower(strings.TrimSpace(preference.Language))
	if preference.Mode != SubtitleOff && preference.Mode != SubtitleAutomatic {
		return ErrInvalidSubtitlePreference
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[profileID]
	if !ok {
		return ErrProfileNotFound
	}
	if m.db != nil {
		if _, err := m.db.Exec("INSERT INTO profile_subtitle_preferences(profile_id,mode,language,prefer_sdh) VALUES(?,?,?,?) ON CONFLICT(profile_id) DO UPDATE SET mode=excluded.mode,language=excluded.language,prefer_sdh=excluded.prefer_sdh", profileID, preference.Mode, preference.Language, boolInt(preference.PreferSDH)); err != nil {
			return fmt.Errorf("save subtitle preference: %w", err)
		}
	}
	p.subtitle = preference
	m.profiles[profileID] = p
	return nil
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
