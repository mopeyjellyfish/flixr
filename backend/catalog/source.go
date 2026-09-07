package catalog

import (
	"fmt"
	"os"
)

// SourceKey identifies the private physical source used to admit playback. It
// never grants filesystem access and is excluded from the public item JSON.
func (x Item) SourceKey() string {
	return id("source", fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d", x.sourceRoot, x.rootKind, x.path, x.digest, x.changeToken, x.size, x.mtime))
}

func (c *Catalog) loadSourceProof() error {
	rows, err := c.db.Query(`SELECT i.id,f.full_digest,f.change_token FROM catalog_items i JOIN catalog_physical_files f ON f.id=i.primary_file_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, digest, token string
		if err := rows.Scan(&id, &digest, &token); err != nil {
			return err
		}
		x := c.items[id]
		x.digest, x.changeToken = digest, token
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
		x.MediaProperties = source.MediaProperties
	}
	return x, ok
}
