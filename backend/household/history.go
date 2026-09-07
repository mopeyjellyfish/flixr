package household

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const historyPageMax = 100
const historyUndoWindow = 5 * time.Minute

type EventType string

const (
	EventCompleted EventType = "completed"
	EventSummary   EventType = "summary"
)

type EventProvenance string

const (
	ProvenanceLocal  EventProvenance = "local"
	ProvenanceImport EventProvenance = "import"
)

type ViewingEvent struct {
	ID         string          `json:"id"`
	CatalogID  string          `json:"catalog_id"`
	Title      string          `json:"title"`
	Kind       string          `json:"kind"`
	Type       EventType       `json:"type"`
	Provenance EventProvenance `json:"provenance"`
	SourceID   string          `json:"source_id,omitempty"`
	SourceTime *int64          `json:"source_time"`
	RecordedAt int64           `json:"recorded_at"`
}
type HistoryPage struct {
	Events []ViewingEvent `json:"events"`
	Next   string         `json:"next,omitempty"`
}
type Rating struct {
	CatalogID  string          `json:"catalog_id"`
	Value      int             `json:"value"`
	Provenance EventProvenance `json:"provenance"`
	SourceID   string          `json:"source_id,omitempty"`
	UpdatedAt  int64           `json:"updated_at"`
}
type HistoryClear struct {
	ID        string `json:"id"`
	UndoUntil int64  `json:"undo_until"`
}

var ErrInvalidHistory = errors.New("invalid history record")
var ErrHistoryClearNotFound = errors.New("history clear not found")

func validProvenance(p EventProvenance) bool { return p == ProvenanceLocal || p == ProvenanceImport }
func (m *Manager) validHistoryProfile(id string) bool {
	m.mu.Lock()
	_, ok := m.profiles[id]
	m.mu.Unlock()
	return ok && id != ""
}
func (m *Manager) RecordViewingEvent(profileID string, event ViewingEvent) error {
	if !m.validHistoryProfile(profileID) || !validViewingEvent(event) {
		return ErrInvalidHistory
	}
	if m.db == nil {
		return nil
	}
	if event.ID == "" {
		var err error
		event.ID, err = random()
		if err != nil {
			return err
		}
	}
	if event.RecordedAt == 0 {
		event.RecordedAt = time.Now().UnixMilli()
	}
	return m.recordViewingEvent(m.db, profileID, event)
}

type eventWriter interface {
	Exec(string, ...any) (sql.Result, error)
}

func validViewingEvent(event ViewingEvent) bool {
	return event.CatalogID != "" && strings.TrimSpace(event.Title) != "" && event.Kind != "" && (event.Type == EventCompleted || event.Type == EventSummary) && validProvenance(event.Provenance) && !(event.Provenance == ProvenanceLocal && event.SourceTime != nil)
}
func (m *Manager) recordViewingEvent(db eventWriter, profileID string, event ViewingEvent) error {
	if !validViewingEvent(event) {
		return ErrInvalidHistory
	}
	if event.ID == "" {
		var err error
		event.ID, err = random()
		if err != nil {
			return err
		}
	}
	_, err := db.Exec(`INSERT INTO viewing_events(event_id,profile_id,catalog_id,title,kind,event_type,provenance,source_id,source_time,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(profile_id,provenance,catalog_id,source_id) WHERE source_id<>'' DO NOTHING`, event.ID, profileID, event.CatalogID, event.Title, event.Kind, event.Type, event.Provenance, event.SourceID, event.SourceTime, event.RecordedAt)
	return err
}
func (m *Manager) SetRating(profileID, catalogID string, value int, provenance EventProvenance, sourceID string) error {
	if !m.validHistoryProfile(profileID) || catalogID == "" || value < 1 || value > 5 || !validProvenance(provenance) {
		return ErrInvalidHistory
	}
	if m.db == nil {
		return nil
	}
	_, err := m.db.Exec(`INSERT INTO profile_ratings(profile_id,catalog_id,rating,provenance,source_id,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(profile_id,catalog_id) DO UPDATE SET rating=excluded.rating,provenance=excluded.provenance,source_id=excluded.source_id,updated_at=excluded.updated_at`, profileID, catalogID, value, provenance, sourceID, time.Now().UnixMilli())
	return err
}
func (m *Manager) DeleteRating(profileID, catalogID string) error {
	if !m.validHistoryProfile(profileID) || catalogID == "" {
		return ErrInvalidHistory
	}
	if m.db == nil {
		return nil
	}
	_, err := m.db.Exec(`DELETE FROM profile_ratings WHERE profile_id=? AND catalog_id=?`, profileID, catalogID)
	return err
}
func (m *Manager) Rating(profileID, catalogID string) (Rating, bool, error) {
	if !m.validHistoryProfile(profileID) || catalogID == "" {
		return Rating{}, false, ErrInvalidHistory
	}
	if m.db == nil {
		return Rating{}, false, nil
	}
	var r Rating
	err := m.db.QueryRow(`SELECT catalog_id,rating,provenance,source_id,updated_at FROM profile_ratings WHERE profile_id=? AND catalog_id=?`, profileID, catalogID).Scan(&r.CatalogID, &r.Value, &r.Provenance, &r.SourceID, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Rating{}, false, nil
	}
	return r, err == nil, err
}

type historyCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

func decodeCursor(value string) (historyCursor, error) {
	if value == "" {
		return historyCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return historyCursor{}, ErrInvalidHistory
	}
	var c historyCursor
	if err = json.Unmarshal(raw, &c); err != nil || c.At < 0 || c.ID == "" {
		return historyCursor{}, ErrInvalidHistory
	}
	return c, nil
}
func encodeCursor(c historyCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (m *Manager) History(profileID string, limit int, before string) (HistoryPage, error) {
	if !m.validHistoryProfile(profileID) {
		return HistoryPage{}, ErrInvalidHistory
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > historyPageMax {
		return HistoryPage{}, ErrInvalidHistory
	}
	c, err := decodeCursor(before)
	if err != nil {
		return HistoryPage{}, err
	}
	if m.db == nil {
		return HistoryPage{Events: []ViewingEvent{}}, nil
	}
	rows, err := m.db.Query(`SELECT e.event_id,e.catalog_id,e.title,e.kind,e.event_type,e.provenance,e.source_id,e.source_time,e.recorded_at FROM viewing_events e WHERE e.profile_id=? AND (e.recorded_at<? OR (e.recorded_at=? AND e.event_id<?)) AND NOT EXISTS (SELECT 1 FROM viewing_history_clears h WHERE h.profile_id=e.profile_id AND h.undone_at=0 AND (e.rowid<=h.cleared_rowid)) ORDER BY e.recorded_at DESC,e.event_id DESC LIMIT ?`, profileID, func() int64 {
		if c.At == 0 {
			return 1 << 62
		}
		return c.At
	}(), func() int64 {
		if c.At == 0 {
			return 1 << 62
		}
		return c.At
	}(), func() string {
		if c.ID == "" {
			return "~"
		}
		return c.ID
	}(), limit+1)
	if err != nil {
		return HistoryPage{}, err
	}
	defer rows.Close()
	p := HistoryPage{Events: []ViewingEvent{}}
	for rows.Next() {
		var e ViewingEvent
		var source sql.NullInt64
		if err := rows.Scan(&e.ID, &e.CatalogID, &e.Title, &e.Kind, &e.Type, &e.Provenance, &e.SourceID, &source, &e.RecordedAt); err != nil {
			return HistoryPage{}, err
		}
		if source.Valid {
			e.SourceTime = &source.Int64
		}
		p.Events = append(p.Events, e)
	}
	if err := rows.Err(); err != nil {
		return HistoryPage{}, err
	}
	if len(p.Events) > limit {
		last := p.Events[limit-1]
		p.Events = p.Events[:limit]
		p.Next = encodeCursor(historyCursor{last.RecordedAt, last.ID})
	}
	return p, nil
}
func (m *Manager) ClearHistory(profileID string) (HistoryClear, error) {
	if !m.validHistoryProfile(profileID) || m.db == nil {
		return HistoryClear{}, ErrInvalidHistory
	}
	id, err := random()
	if err != nil {
		return HistoryClear{}, err
	}
	now := time.Now().UnixMilli()
	var cutoffRowID int64
	_ = m.db.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM viewing_events WHERE profile_id=?`, profileID).Scan(&cutoffRowID)
	clear := HistoryClear{ID: id, UndoUntil: now + historyUndoWindow.Milliseconds()}
	_, err = m.db.Exec(`INSERT INTO viewing_history_clears(clear_id,profile_id,cleared_at,cleared_rowid,undo_until) VALUES(?,?,?,?,?)`, id, profileID, now, cutoffRowID, clear.UndoUntil)
	return clear, err
}
func (m *Manager) UndoClearHistory(profileID, clearID string) error {
	if !m.validHistoryProfile(profileID) || clearID == "" || m.db == nil {
		return ErrInvalidHistory
	}
	r, err := m.db.Exec(`UPDATE viewing_history_clears SET undone_at=? WHERE clear_id=? AND profile_id=? AND undone_at=0 AND undo_until>=?`, time.Now().UnixMilli(), clearID, profileID, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrHistoryClearNotFound
	}
	return nil
}
