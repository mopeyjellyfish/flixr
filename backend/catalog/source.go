package catalog

import (
	"context"
	"fmt"
	"os"
)

// SourceKey identifies the private physical source used to admit playback. It
// never grants filesystem access and is excluded from the public item JSON.
func (x Item) SourceKey() string {
	return id("source", fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d", x.sourceRoot, x.rootKind, x.path, x.digest, x.changeToken, x.size, x.mtime))
}

func (c *Catalog) loadSourceProof() error {
	rows, err := c.db.Query(`SELECT i.id,f.full_digest,f.change_token,f.source_series_id FROM catalog_items i JOIN catalog_physical_files f ON f.id=i.primary_file_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, digest, token, sourceSeries string
		if err := rows.Scan(&id, &digest, &token, &sourceSeries); err != nil {
			return err
		}
		x := c.items[id]
		x.digest, x.changeToken, x.sourceSeriesID = digest, token, sourceSeries
		c.items[id] = x
	}
	return rows.Err()
}

// OpenSource refuses a source change instead of silently changing the media for
// an admitted session. os.Root confines every reopen, including symlink races.
func (c *Catalog) OpenSource(id, key string) (*os.File, error) {
	c.mu.RLock()
	x, ok := c.playbackSource(id)
	root := c.film
	if x.rootKind == "episode" {
		root = c.tv
	}
	x.sourceRoot = root
	c.mu.RUnlock()
	if !ok || !x.Playable || (key != "" && x.SourceKey() != key) {
		return nil, os.ErrNotExist
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	file, err := r.Open(x.path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if x.probeRevision != 0 && (info.Size() != x.size || info.ModTime().UnixNano() != x.mtime || fileChangeToken(info) != x.changeToken) {
		file.Close()
		return nil, os.ErrNotExist
	}
	return file, nil
}

// Called under mu. Keep owner metadata on the survivor while opening a source
// retained by an active repair when the survivor has no physical file.
func (c *Catalog) playbackSource(id string) (Item, bool) {
	x, ok := c.items[id]
	if !ok || c.db == nil {
		return x, ok
	}
	var sourceID string
	err := c.db.QueryRow(`SELECT m.source_catalog_id FROM catalog_identity_merges m JOIN catalog_items survivor ON survivor.id=m.survivor_catalog_id JOIN catalog_items source ON source.id=m.source_catalog_id WHERE m.survivor_catalog_id=? AND m.state='active' AND m.kind<>'series' AND survivor.available=0 AND source.available=1`, id).Scan(&sourceID)
	if err == nil {
		source := c.items[sourceID]
		x.path, x.rootKind, x.digest, x.changeToken, x.size, x.mtime, x.probeRevision = source.path, source.rootKind, source.digest, source.changeToken, source.size, source.mtime, source.probeRevision
		x.fingerprint = source.fingerprint
		x.MediaProperties = source.MediaProperties
	}
	return x, ok
}

// Legacy rows have no durable full digest yet. Establish proof only for the
// still-unchanged, confined file before admitting its first playback. This avoids
// forcing a library-wide rescan after upgrade and never grants a wildcard lease.
func (c *Catalog) hydrateLegacySource(ctx context.Context, id string) error {
	c.mu.RLock()
	x, ok := c.playbackSource(id)
	root := c.film
	if x.rootKind == "episode" {
		root = c.tv
	}
	x.sourceRoot = root
	c.mu.RUnlock()
	if !ok || !x.Playable || x.probeRevision == 0 || x.digest != "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer confined.Close()
	file, err := confined.Open(x.path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != x.size || info.ModTime().UnixNano() != x.mtime {
		return ErrNotPlayable
	}
	fingerprint, err := contentFingerprint(file)
	if err != nil {
		return err
	}
	if fingerprint != x.fingerprint {
		return ErrNotPlayable
	}
	digest, err := digestFile(ctx, file)
	if err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil {
		return err
	}
	token := fileChangeToken(after)
	if after.Size() != info.Size() || after.ModTime() != info.ModTime() || token != fileChangeToken(info) {
		return ErrNotPlayable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scanning {
		return ErrMetadataBusy
	}
	current, ok := c.playbackSource(id)
	current.sourceRoot = c.film
	if current.rootKind == "episode" {
		current.sourceRoot = c.tv
	}
	if !ok {
		return ErrNotPlayable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if current.SourceKey() != x.SourceKey() {
		proven := x
		proven.digest, proven.changeToken = digest, token
		if current.SourceKey() == proven.SourceKey() {
			return nil
		}
		return ErrNotPlayable
	}
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var physicalID, sourceID string
	if err := tx.QueryRowContext(ctx, `SELECT id,catalog_id FROM catalog_physical_files WHERE root_kind=? AND relative_path=? AND present=1`, x.rootKind, x.path).Scan(&physicalID, &sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_physical_files SET full_digest=?,change_token=? WHERE id=?`, digest, token, physicalID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	source := c.items[sourceID]
	source.digest, source.changeToken = digest, token
	c.items[sourceID] = source
	return nil
}
