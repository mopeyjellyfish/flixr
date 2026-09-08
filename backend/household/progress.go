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
// An expected generation makes admission conditional on the original snapshot.
// The durable generation also fences old sessions after restart or a manual action.
func (m *Manager) BeginPlayback(profileID, catalogID string, expected ...int64) (generation, position int64, err error) {
	if !m.validProgress(profileID, catalogID, 0) {
		return 0, 0, ErrCredentials
	}
	if m.db == nil {
		return 0, 0, nil
	}
	compare := int64(-1)
	if len(expected) > 0 {
		compare = expected[0]
		if compare < 0 {
			return 0, 0, ErrProgressConflict
		}
	}
	if compare > 0 {
		err = m.db.Writer().QueryRow(`UPDATE progress SET generation=generation+1,observation=0,completion_id='' WHERE profile_id=? AND catalog_id=? AND generation=? RETURNING generation,CASE WHEN completed=1 THEN 0 ELSE position_ms END`, profileID, catalogID, compare).Scan(&generation, &position)
	} else {
		err = m.db.Writer().QueryRow(`INSERT INTO progress(profile_id,catalog_id,position_ms,generation) VALUES(?,?,0,1)
 ON CONFLICT(profile_id,catalog_id) DO UPDATE SET generation=progress.generation+1,observation=0,completion_id='' WHERE ?=-1 OR progress.generation=?
 RETURNING generation,CASE WHEN completed=1 THEN 0 ELSE position_ms END`, profileID, catalogID, compare, compare).Scan(&generation, &position)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrProgressConflict
	}

	return
}

// RecordPlaybackProgress compares observations only within a server generation.
// Zero observations support older players in receipt order. Neither observations
// nor legacy client clocks are ever used as durable wall-clock timestamps.
func (m *Manager) RecordPlaybackProgress(profileID, catalogID string, position, generation, observation int64, completed bool, completion ...*ViewingEvent) (bool, error) {
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
	completionID := ""
	if completed && len(completion) > 0 && completion[0] != nil {
		var tokenErr error
		completionID, tokenErr = random()
		if tokenErr != nil {
			return false, tokenErr
		}
	}
	tx, err := m.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE progress SET position_ms=?,updated_at=?,completed=CASE WHEN observation=0 THEN ? ELSE MAX(completed,?) END,completed_at=CASE WHEN observation>0 AND completed=1 THEN completed_at ELSE ? END,completion_id=CASE WHEN completion_id='' AND ?=1 THEN ? ELSE completion_id END,observation=CASE WHEN ?=0 THEN observation+1 ELSE ? END
 WHERE profile_id=? AND catalog_id=? AND generation=? AND (?=0 OR observation<?)`, position, now, boolInt(completed), boolInt(completed), completedAt, boolInt(completed), completionID, observation, observation, profileID, catalogID, generation, observation, observation)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return rows == 1, err
	}
	if completed && len(completion) > 0 && completion[0] != nil {
		e := *completion[0]
		if e.SourceID == "" {
			if err := tx.QueryRow(`SELECT completion_id FROM progress WHERE profile_id=? AND catalog_id=? AND generation=?`, profileID, catalogID, generation).Scan(&e.SourceID); err != nil {
				return false, err
			}
			if e.SourceID == "" {
				return false, ErrProgressConflict
			}
		}
		if e.Type == "" {
			e.Type = EventCompleted
		}
		if e.Provenance == "" {
			e.Provenance = ProvenanceLocal
		}
		if err := m.recordViewingEvent(tx, profileID, e); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CanRecordPlaybackProgress reports whether an observation still has authority
// without changing durable progress. The eventual write remains a compare-and-
// swap so a concurrent newer observation can never be overwritten.
func (m *Manager) CanRecordPlaybackProgress(profileID, catalogID string, generation, observation int64) (bool, error) {
	if !m.validProgress(profileID, catalogID, 0) || generation < 0 || observation < 0 {
		return false, ErrCredentials
	}
	if m.db == nil {
		return true, nil
	}
	var currentGeneration, currentObservation int64
	err := m.db.QueryRow(`SELECT generation,observation FROM progress WHERE profile_id=? AND catalog_id=?`, profileID, catalogID).Scan(&currentGeneration, &currentObservation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return currentGeneration == generation && (observation == 0 || currentObservation < observation), nil
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
		result, err = m.db.Exec(`UPDATE progress SET position_ms=?,updated_at=?,completed=?,completed_at=?,generation=generation+1,observation=0,completion_id='' WHERE profile_id=? AND catalog_id=? AND generation=?`, position, now, boolInt(completed), completedAt, profileID, catalogID, generation)
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
