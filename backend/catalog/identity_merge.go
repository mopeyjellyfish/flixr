package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// MergeIdentity retains both anchors and their records. The chosen survivor's
// progress wins when both exist; source-only progress is copied with provenance.
// Current generations are fenced so a pre-repair player cannot overwrite it.
func (c *Catalog) MergeIdentity(kind, survivorID, sourceID string) (IdentityMerge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return IdentityMerge{}, ErrMetadataBusy
	}
	survivor, ok := c.identityTarget(kind, survivorID)
	if !ok {
		return IdentityMerge{}, ErrMetadataNotFound
	}
	source, ok := c.identityTarget(kind, sourceID)
	if !ok {
		return IdentityMerge{}, ErrMetadataNotFound
	}
	if c.db == nil || survivorID == sourceID {
		return IdentityMerge{}, ErrIdentityConflict
	}
	mergeID, err := randomScanID()
	if err != nil {
		return IdentityMerge{}, err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return IdentityMerge{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM catalog_identity_merges WHERE state='active' AND (survivor_catalog_id IN (?,?) OR source_catalog_id IN (?,?))`, survivorID, sourceID, survivorID, sourceID).Scan(&count); err != nil {
		return IdentityMerge{}, err
	}
	if count != 0 {
		return IdentityMerge{}, ErrIdentityConflict
	}
	decisions := c.identityAffectedDecisions(kind, survivorID, sourceID)
	decisions = append(decisions, "Both original title records are retained. Existing survivor progress is kept; source-only progress is copied.")
	encodedDecisions, err := json.Marshal(decisions)
	if err != nil {
		return IdentityMerge{}, err
	}
	if _, err := tx.Exec(`INSERT INTO catalog_identity_merges(id,kind,survivor_catalog_id,source_catalog_id,created_at,decisions_json) VALUES(?,?,?,?,?,?)`, mergeID, kind, survivorID, sourceID, time.Now().UnixMilli(), string(encodedDecisions)); err != nil {
		return IdentityMerge{}, err
	}
	if err := snapshotContinueWatchingDismissals(tx, mergeID, "before", kind, survivorID, sourceID); err != nil {
		return IdentityMerge{}, err
	}
	if kind == "series" {
		if _, err := tx.Exec(`INSERT INTO catalog_identity_episode_snapshots SELECT ?,id,series_id,season_id FROM catalog_items WHERE series_id=?`, mergeID, sourceID); err != nil {
			return IdentityMerge{}, err
		}
	} else {
		if err := snapshotProgress(tx, mergeID, "before", survivorID, sourceID); err != nil {
			return IdentityMerge{}, err
		}
		if _, err := tx.Exec(`INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation,observation,completion_id) SELECT profile_id,?,position_ms,updated_at,completed,completed_at,generation,observation,completion_id FROM progress WHERE catalog_id=? ON CONFLICT DO NOTHING`, survivorID, sourceID); err != nil {
			return IdentityMerge{}, err
		}
		if _, err := tx.Exec(`UPDATE progress SET generation=generation+1,observation=0 WHERE catalog_id IN (?,?)`, survivorID, sourceID); err != nil {
			return IdentityMerge{}, err
		}
		if err := snapshotProgress(tx, mergeID, "after", survivorID, sourceID); err != nil {
			return IdentityMerge{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE catalog_identity_conflicts SET state='merged',resolved_at=? WHERE kind=? AND ((left_catalog_id=? AND right_catalog_id=?) OR (left_catalog_id=? AND right_catalog_id=?)) AND state='open'`, time.Now().UnixMilli(), kind, survivorID, sourceID, sourceID, survivorID); err != nil {
		return IdentityMerge{}, err
	}
	if err := applyIdentityMappings(tx); err != nil {
		return IdentityMerge{}, err
	}
	if err := snapshotContinueWatchingDismissals(tx, mergeID, "after", kind, survivorID, sourceID); err != nil {
		return IdentityMerge{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdentityMerge{}, err
	}
	c.applyIdentityMemory(kind, survivorID, sourceID, true)
	return IdentityMerge{ID: mergeID, Kind: kind, State: "active", Survivor: survivor, Source: source, Decisions: decisions}, nil
}

func snapshotProgress(tx *sql.Tx, mergeID, phase, survivor, source string) error {
	_, err := tx.Exec(`INSERT INTO catalog_identity_progress_snapshots(merge_id,phase,profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation,observation,completion_id) SELECT ?,?,profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation,observation,completion_id FROM progress WHERE catalog_id IN (?,?)`, mergeID, phase, survivor, source)
	return err
}

type identityProgress struct {
	profile, id, completionID                                          string
	position, updated, completed, completedAt, generation, observation int64
}

func (c *Catalog) UnmergeIdentity(mergeID string) (IdentityMerge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return IdentityMerge{}, ErrMetadataBusy
	}
	if c.db == nil {
		return IdentityMerge{}, ErrMetadataNotFound
	}
	tx, err := c.db.Begin()
	if err != nil {
		return IdentityMerge{}, err
	}
	defer tx.Rollback()
	var v IdentityMerge
	var survivor, source string
	err = tx.QueryRow(`SELECT id,kind,survivor_catalog_id,source_catalog_id,state FROM catalog_identity_merges WHERE id=?`, mergeID).Scan(&v.ID, &v.Kind, &survivor, &source, &v.State)
	if err == sql.ErrNoRows {
		return v, ErrMetadataNotFound
	}
	if err != nil {
		return v, err
	}
	if v.State != "active" {
		return v, ErrIdentityConflict
	}
	v.Survivor, _ = c.identityTarget(v.Kind, survivor)
	v.Source, _ = c.identityTarget(v.Kind, source)
	v.Decisions = c.identityAffectedDecisions(v.Kind, survivor, source)
	rows, err := tx.Query(`SELECT profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation,observation,completion_id FROM catalog_identity_progress_snapshots WHERE merge_id=? AND phase='after'`, mergeID)
	if err != nil {
		return v, err
	}
	var after []identityProgress
	for rows.Next() {
		var p identityProgress
		if err := rows.Scan(&p.profile, &p.id, &p.position, &p.updated, &p.completed, &p.completedAt, &p.generation, &p.observation, &p.completionID); err != nil {
			rows.Close()
			return v, err
		}
		after = append(after, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return v, err
	}
	for _, p := range after {
		var same int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM progress WHERE profile_id=? AND catalog_id=? AND position_ms=? AND updated_at=? AND completed=? AND completed_at=? AND generation=? AND observation=? AND completion_id=?`, p.profile, p.id, p.position, p.updated, p.completed, p.completedAt, p.generation, p.observation, p.completionID).Scan(&same); err != nil {
			return v, err
		}
		if same == 0 {
			v.Decisions = append(v.Decisions, fmt.Sprintf("Retained newer viewing state for profile %s on title %s.", p.profile, p.id))
			continue
		}
		var before identityProgress
		err := tx.QueryRow(`SELECT position_ms,updated_at,completed,completed_at,completion_id FROM catalog_identity_progress_snapshots WHERE merge_id=? AND phase='before' AND profile_id=? AND catalog_id=?`, mergeID, p.profile, p.id).Scan(&before.position, &before.updated, &before.completed, &before.completedAt, &before.completionID)
		if err == sql.ErrNoRows {
			// Retain a zero tombstone with a newer generation to fence old sessions.
			_, err = tx.Exec(`UPDATE progress SET position_ms=0,updated_at=0,completed=0,completed_at=0,completion_id='',generation=generation+1,observation=0 WHERE profile_id=? AND catalog_id=?`, p.profile, p.id)
		} else if err == nil {
			_, err = tx.Exec(`UPDATE progress SET position_ms=?,updated_at=?,completed=?,completed_at=?,completion_id=?,generation=generation+1,observation=0 WHERE profile_id=? AND catalog_id=?`, before.position, before.updated, before.completed, before.completedAt, before.completionID, p.profile, p.id)
		}
		if err != nil {
			return v, err
		}
	}
	if v.Kind == "series" {
		if _, err := tx.Exec(`UPDATE catalog_items SET series_id=(SELECT series_id FROM catalog_identity_episode_snapshots s WHERE s.merge_id=? AND s.catalog_id=catalog_items.id),season_id=(SELECT season_id FROM catalog_identity_episode_snapshots s WHERE s.merge_id=? AND s.catalog_id=catalog_items.id) WHERE id IN (SELECT catalog_id FROM catalog_identity_episode_snapshots WHERE merge_id=?)`, mergeID, mergeID, mergeID); err != nil {
			return v, err
		}
	}
	if err := reconcileContinueWatchingDismissals(tx, mergeID, v.Kind, survivor, source); err != nil {
		return v, err
	}
	decisions, err := json.Marshal(v.Decisions)
	if err != nil {
		return v, err
	}
	if _, err := tx.Exec(`UPDATE catalog_identity_merges SET state='unmerged',unmerged_at=?,decisions_json=? WHERE id=?`, time.Now().UnixMilli(), string(decisions), mergeID); err != nil {
		return v, err
	}
	table := "catalog_items"
	if v.Kind == "series" {
		table = "catalog_series"
	}
	if _, err := tx.Exec(`UPDATE `+table+` SET merged_into='' WHERE id=?`, source); err != nil {
		return v, err
	}
	if v.Kind != "series" {
		if _, err := tx.Exec(`UPDATE catalog_items SET playable=available WHERE id IN (?,?)`, source, survivor); err != nil {
			return v, err
		}
	}
	if err := applyIdentityMappings(tx); err != nil {
		return v, err
	}
	if err := tx.Commit(); err != nil {
		return v, err
	}
	c.applyIdentityMemory(v.Kind, survivor, source, false)
	v.State = "unmerged"
	return v, nil
}

func applyIdentityMappings(tx *sql.Tx) error {
	// New episodes discovered while a series repair is active need the same
	// original ownership snapshot as episodes present at the initial merge.
	if _, err := tx.Exec(`INSERT INTO catalog_identity_episode_snapshots(merge_id,catalog_id,series_id,season_id) SELECT m.id,i.id,i.series_id,i.season_id FROM catalog_items i JOIN catalog_identity_merges m ON m.source_catalog_id=i.series_id AND m.kind='series' AND m.state='active' WHERE 1 ON CONFLICT(merge_id,catalog_id) DO NOTHING`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO profile_continue_watching_dismissals(profile_id,catalog_kind,catalog_id,dismissed_at)
		SELECT d.profile_id,d.catalog_kind,m.survivor_catalog_id,d.dismissed_at
		FROM profile_continue_watching_dismissals d JOIN catalog_identity_merges m ON m.source_catalog_id=d.catalog_id AND m.kind=d.catalog_kind AND m.state='active'
		ON CONFLICT(profile_id,catalog_kind,catalog_id) DO UPDATE SET dismissed_at=MAX(profile_continue_watching_dismissals.dismissed_at,excluded.dismissed_at);
		DELETE FROM profile_continue_watching_dismissals WHERE EXISTS (
			SELECT 1 FROM catalog_identity_merges m WHERE m.source_catalog_id=profile_continue_watching_dismissals.catalog_id AND m.kind=profile_continue_watching_dismissals.catalog_kind AND m.state='active'
		)`); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE catalog_items SET merged_into=COALESCE((SELECT survivor_catalog_id FROM catalog_identity_merges m WHERE m.source_catalog_id=catalog_items.id AND m.state='active' AND m.kind<>'series'),''); UPDATE catalog_items SET playable=CASE WHEN merged_into<>'' THEN 0 WHEN available=1 THEN 1 ELSE EXISTS(SELECT 1 FROM catalog_identity_merges m JOIN catalog_items source ON source.id=m.source_catalog_id WHERE m.survivor_catalog_id=catalog_items.id AND m.state='active' AND m.kind<>'series' AND source.available=1) END; UPDATE catalog_series SET merged_into=COALESCE((SELECT survivor_catalog_id FROM catalog_identity_merges m WHERE m.source_catalog_id=catalog_series.id AND m.state='active' AND m.kind='series'),''); UPDATE catalog_items SET series_id=(SELECT survivor_catalog_id FROM catalog_identity_merges m WHERE m.source_catalog_id=catalog_items.series_id AND m.state='active' AND m.kind='series') WHERE series_id IN (SELECT source_catalog_id FROM catalog_identity_merges WHERE state='active' AND kind='series'); UPDATE catalog_artwork_retries SET parent_catalog_id=(SELECT series_id FROM catalog_items WHERE id=catalog_artwork_retries.catalog_id),parent_provider_id=(SELECT provider_id FROM catalog_series WHERE id=(SELECT series_id FROM catalog_items WHERE id=catalog_artwork_retries.catalog_id)) WHERE catalog_kind='episode'; UPDATE catalog_artwork_reconciliations SET parent_catalog_id=(SELECT series_id FROM catalog_items WHERE id=catalog_artwork_reconciliations.catalog_id),parent_provider_id=(SELECT provider_id FROM catalog_series WHERE id=(SELECT series_id FROM catalog_items WHERE id=catalog_artwork_reconciliations.catalog_id)) WHERE catalog_kind='episode'; UPDATE catalog_series SET playable=EXISTS(SELECT 1 FROM catalog_items i WHERE i.series_id=catalog_series.id AND i.playable=1) WHERE demo=0`)
	return err
}

func snapshotContinueWatchingDismissals(tx *sql.Tx, mergeID, phase, kind, survivor, source string) error {
	if kind != "film" && kind != "series" {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO catalog_identity_continue_watching_snapshots(merge_id,phase,profile_id,catalog_kind,catalog_id,dismissed_at)
		SELECT ?,?,profile_id,catalog_kind,catalog_id,dismissed_at FROM profile_continue_watching_dismissals
		WHERE catalog_kind=? AND catalog_id IN (?,?)`, mergeID, phase, kind, survivor, source)
	return err
}

func reconcileContinueWatchingDismissals(tx *sql.Tx, mergeID, kind, survivor, source string) error {
	if kind != "film" && kind != "series" {
		return nil
	}
	rows, err := tx.Query(`SELECT p.profile_id,
		(SELECT dismissed_at FROM catalog_identity_continue_watching_snapshots s WHERE s.merge_id=? AND s.phase='before' AND s.profile_id=p.profile_id AND s.catalog_kind=? AND s.catalog_id=?),
		(SELECT dismissed_at FROM catalog_identity_continue_watching_snapshots s WHERE s.merge_id=? AND s.phase='before' AND s.profile_id=p.profile_id AND s.catalog_kind=? AND s.catalog_id=?),
		(SELECT dismissed_at FROM catalog_identity_continue_watching_snapshots s WHERE s.merge_id=? AND s.phase='after' AND s.profile_id=p.profile_id AND s.catalog_kind=? AND s.catalog_id=?),
		(SELECT dismissed_at FROM profile_continue_watching_dismissals d WHERE d.profile_id=p.profile_id AND d.catalog_kind=? AND d.catalog_id=?)
		FROM (SELECT profile_id FROM catalog_identity_continue_watching_snapshots WHERE merge_id=? UNION SELECT profile_id FROM profile_continue_watching_dismissals WHERE catalog_kind=? AND catalog_id IN (?,?)) p`,
		mergeID, kind, survivor, mergeID, kind, source, mergeID, kind, survivor, kind, survivor, mergeID, kind, survivor, source)
	if err != nil {
		return err
	}
	type state struct {
		profile                                      string
		beforeSurvivor, beforeSource, after, current sql.NullInt64
	}
	var states []state
	for rows.Next() {
		var value state
		if err := rows.Scan(&value.profile, &value.beforeSurvivor, &value.beforeSource, &value.after, &value.current); err != nil {
			rows.Close()
			return err
		}
		states = append(states, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, value := range states {
		if _, err := tx.Exec(`DELETE FROM profile_continue_watching_dismissals WHERE profile_id=? AND catalog_kind=? AND catalog_id IN (?,?)`, value.profile, kind, survivor, source); err != nil {
			return err
		}
		if value.current.Valid != value.after.Valid || (value.current.Valid && value.current.Int64 != value.after.Int64) {
			if value.current.Valid {
				if _, err := tx.Exec(`INSERT INTO profile_continue_watching_dismissals(profile_id,catalog_kind,catalog_id,dismissed_at) VALUES(?,?,?,?),(?,?,?,?)`, value.profile, kind, survivor, value.current.Int64, value.profile, kind, source, value.current.Int64); err != nil {
					return err
				}
			}
			continue
		}
		for id, dismissed := range map[string]sql.NullInt64{survivor: value.beforeSurvivor, source: value.beforeSource} {
			if dismissed.Valid {
				if _, err := tx.Exec(`INSERT INTO profile_continue_watching_dismissals(profile_id,catalog_kind,catalog_id,dismissed_at) VALUES(?,?,?,?)`, value.profile, kind, id, dismissed.Int64); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (c *Catalog) applyIdentityMemory(kind, survivor, source string, merged bool) {
	defer c.refreshSeriesAvailability()
	if kind != "series" {
		for _, id := range []string{survivor, source} {
			item := c.items[id]
			var playable bool
			if c.db.QueryRow(`SELECT playable FROM catalog_items WHERE id=?`, id).Scan(&playable) == nil {
				item.Playable = playable
			}
			c.items[id] = item
		}
		return
	}
	rows, err := c.db.Query(`SELECT id,series_id FROM catalog_items`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, series string
		if rows.Scan(&id, &series) != nil {
			return
		}
		item := c.items[id]
		item.SeriesID = series
		c.items[id] = item
	}
}

func (c *Catalog) identityAffectedDecisions(kind, survivor, source string) []string {
	out := []string{}
	if kind != "series" {
		return out
	}
	for _, item := range c.items {
		if item.SeriesID == survivor || item.SeriesID == source {
			out = append(out, fmt.Sprintf("Affected episode: %s (S%02dE%02d, %s).", item.Title, item.Season, item.Episode, item.ID))
		}
	}
	sort.Strings(out)
	return out
}

// Called under mu after a successful identity transaction.
func (c *Catalog) refreshSeriesAvailability() {
	if c.db == nil {
		return
	}
	rows, err := c.db.Query(`SELECT id,playable FROM catalog_series`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var playable bool
		if rows.Scan(&id, &playable) != nil {
			return
		}
		item := c.series[id]
		item.Playable = playable
		c.series[id] = item
	}
}
