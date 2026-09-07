package household

import (
	"database/sql"
	"errors"
	"time"
)

var ErrProgressConflict = errors.New("progress generation changed")

func (m *Manager) validProgress(profileID, catalogID string, position int64) bool {
	m.mu.Lock()
	_, ok := m.profiles[profileID]
	m.mu.Unlock()
	return ok && profileID != "" && catalogID != "" && position >= 0
}

// BeginPlayback gives a new playback exclusive progress authority for this item.
// The durable generation also fences old sessions after restart or a manual action.
func (m *Manager) BeginPlayback(profileID, catalogID string) (generation, position int64, err error) {
	if !m.validProgress(profileID, catalogID, 0) {
		return 0, 0, ErrCredentials
	}
	if m.db == nil {
		return 0, 0, nil
	}
	err = m.db.Writer().QueryRow(`INSERT INTO progress(profile_id,catalog_id,position_ms,generation) VALUES(?,?,0,1)
 ON CONFLICT(profile_id,catalog_id) DO UPDATE SET generation=progress.generation+1,observation=0
 RETURNING generation,CASE WHEN completed=1 THEN 0 ELSE position_ms END`, profileID, catalogID).Scan(&generation, &position)
	return
}

// RecordPlaybackProgress compares observations only within a server generation.
// Zero observations support older players in receipt order. Neither observations
// nor legacy client clocks are ever used as durable wall-clock timestamps.
func (m *Manager) RecordPlaybackProgress(profileID, catalogID string, position, generation, observation int64, completed bool) (bool, error) {
	if !m.validProgress(profileID, catalogID, position) || generation < 0 || observation < 0 {
		return false, ErrCredentials
	}
	if m.db == nil {
		return true, nil
	}
	now := time.Now().UnixMilli()
	completedAt := int64(0)
	if completed {
		completedAt = now
	}
	result, err := m.db.Exec(`UPDATE progress SET position_ms=?,updated_at=?,completed=CASE WHEN observation=0 THEN ? ELSE MAX(completed,?) END,completed_at=CASE WHEN observation>0 AND completed=1 THEN completed_at ELSE ? END,observation=CASE WHEN ?=0 THEN observation+1 ELSE ? END
 WHERE profile_id=? AND catalog_id=? AND generation=? AND (?=0 OR observation<?)`, position, now, boolInt(completed), boolInt(completed), completedAt, observation, observation, profileID, catalogID, generation, observation, observation)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// ProgressForProfile is a trusted server action. Session cleanup must not call it:
// a cached position has no authority to start a new progress generation.
func (m *Manager) ProgressForProfile(profileID, catalogID string, position int64) error {
	generation, _, err := m.BeginPlayback(profileID, catalogID)
	if err != nil {
		return err
	}
	_, err = m.RecordPlaybackProgress(profileID, catalogID, position, generation, 1, false)
	return err
}

// RecordProgress supports the legacy API with optimistic concurrency. Missing
// generations mean zero (initial/migrated state); observedAt is retained solely
// for source compatibility. Every accepted write invalidates its predecessor.
func (m *Manager) RecordProgress(profileID, catalogID string, position, observedAt int64, completed bool, expected ...int64) error {
	if !m.validProgress(profileID, catalogID, position) {
		return ErrCredentials
	}
	if m.db == nil {
		return nil
	}
	generation := int64(0)
	if len(expected) > 0 {
		generation = expected[0]
	}
	if generation < 0 {
		return ErrProgressConflict
	}
	now := time.Now().UnixMilli()
	completedAt := int64(0)
	if completed {
		completedAt = now
	}
	var result sql.Result
	var err error
	if generation == 0 {
		result, err = m.db.Exec(`INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation)
 VALUES(?,?,?,?,?,?,1)
 ON CONFLICT(profile_id,catalog_id) DO UPDATE SET position_ms=excluded.position_ms,updated_at=excluded.updated_at,completed=excluded.completed,completed_at=excluded.completed_at,generation=1,observation=0 WHERE progress.generation=0`, profileID, catalogID, position, now, boolInt(completed), completedAt)
	} else {
		result, err = m.db.Exec(`UPDATE progress SET position_ms=?,updated_at=?,completed=?,completed_at=?,generation=generation+1,observation=0 WHERE profile_id=? AND catalog_id=? AND generation=?`, position, now, boolInt(completed), completedAt, profileID, catalogID, generation)
	}

	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrProgressConflict
	}
	return nil
}
