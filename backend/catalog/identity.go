package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

var ErrIdentityConflict = errors.New("identity repair conflicts with existing state")

type IdentityRepairs struct {
	Conflicts []IdentityConflict `json:"conflicts"`
	Merges    []IdentityMerge    `json:"merges"`
}
type IdentityConflict struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	State  string `json:"state"`
	Left   Item   `json:"left"`
	Right  Item   `json:"right"`
}
type IdentityMerge struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	State     string   `json:"state"`
	Survivor  Item     `json:"survivor"`
	Source    Item     `json:"source"`
	Decisions []string `json:"decisions"`
}
type identityPair struct{ kind, reason, left, right string }

// Only known namespaces and media types can supply an identity key. Provider
// display strings remain metadata even when they cannot prove identity.
func providerIdentity(provider, kind, value string) string {
	if provider != "tmdb" || (kind != "film" && kind != "series") || value == "" {
		return ""
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return provider + ":" + kind + ":" + strconv.FormatUint(n, 10)
}

func (c *Catalog) loadPhysicalSources(previous map[scanKey]Item) ([]Item, error) {
	var proof []Item
	if c.db == nil {
		return nil, nil
	}
	rows, err := c.db.Query(`SELECT catalog_id,root_kind,relative_path,fingerprint,full_digest,change_token,source_series_id,size_bytes,mtime_unix FROM catalog_physical_files ORDER BY present,last_seen,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var catalogID, root, path, fp, digest, token, sourceSeries string
		var size, mtime int64
		if err := rows.Scan(&catalogID, &root, &path, &fp, &digest, &token, &sourceSeries, &size, &mtime); err != nil {
			return nil, err
		}
		c.mu.RLock()
		item, ok := c.items[catalogID]
		c.mu.RUnlock()
		if !ok {
			continue
		}
		item.path, item.rootKind, item.fingerprint, item.digest, item.changeToken, item.size, item.mtime = path, root, fp, digest, token, size, mtime
		item.sourceSeriesID = sourceSeries
		previous[scanKey{root, path}] = item
		proof = append(proof, item)
	}
	return proof, rows.Err()
}

func contradictoryIdentity(old, next Item) bool {
	if old.Kind != next.Kind {
		return true
	}
	if old.Kind == "episode" && (old.Season != next.Season || old.Episode != next.Episode || old.sourceSeriesID != next.sourceSeriesID) {
		return true
	}
	a, b := providerIdentity(old.Provider, old.Kind, old.ProviderID), providerIdentity(next.Provider, next.Kind, next.ProviderID)
	return a != "" && b != "" && a != b
}

func (c *Catalog) reconcileIdentity(ctx context.Context, results []scanResult, previous map[scanKey]Item, physicalProof []Item) (map[string]Item, map[scanKey]Item, []identityPair, error) {
	next := map[string]Item{}
	sources := map[scanKey]Item{}
	var conflicts []identityPair
	c.mu.RLock()
	provider, token := c.provider, c.token
	primaries := make(map[string]scanKey, len(c.items))
	anchors := append([]Item(nil), physicalProof...)
	for id, item := range c.items {
		primaries[id] = scanKey{item.rootKind, item.path}
		anchors = append(anchors, item)
	}
	knownSeries := make(map[string]bool, len(c.series))
	for id := range c.series {
		knownSeries[id] = true
	}
	c.mu.RUnlock()
	discovered := map[scanKey]bool{}
	for _, result := range results {
		discovered[scanKey{result.item.rootKind, result.item.path}] = true
	}
	for _, result := range results {
		item := result.item
		key := scanKey{item.rootKind, item.path}
		old, samePath := previous[key]
		// Fresh lookup is evidence only: owner metadata is applied after reconciliation.
		if samePath && old.digest != item.digest && providerIdentity(old.Provider, old.Kind, old.ProviderID) != "" && provider != nil && token != "" {
			enrichment, err := provider.Lookup(ctx, token, item.Kind, item.Title)
			if ctx.Err() != nil {
				return nil, nil, nil, ctx.Err()
			}
			if err == nil {
				item.Provider, item.ProviderID = "tmdb", enrichment.ProviderID
			}
		}
		if samePath && !contradictoryIdentity(old, item) {
			item.ID, item.AddedAt, item.SeriesID = old.ID, old.AddedAt, old.SeriesID
			// An unavailable primary becomes playable again after successful inspection.
			item.Playable = true
		} else {
			// IDs are anchors, never sampled digest aliases. Find a full-proof candidate
			// among both persisted sources and candidates discovered in this scan.
			item.ID = id(item.Kind, item.fingerprint+"\x00"+item.path+"\x00"+item.digest)
			candidates := map[string]Item{}
			for _, candidate := range anchors {
				if candidate.rootKind == item.rootKind && candidate.fingerprint == item.fingerprint {
					if existing, ok := candidates[candidate.ID]; !ok || candidate.digest == item.digest || existing.digest != item.digest {
						candidates[candidate.ID] = candidate
					}
				}
			}
			for _, candidate := range previous {
				if candidate.rootKind == item.rootKind && candidate.fingerprint == item.fingerprint {
					if existing, ok := candidates[candidate.ID]; !ok || candidate.digest == item.digest || existing.digest != item.digest {
						candidates[candidate.ID] = candidate
					}
				}
			}
			for _, candidate := range next {
				if candidate.rootKind == item.rootKind && candidate.fingerprint == item.fingerprint {
					if existing, ok := candidates[candidate.ID]; !ok || candidate.digest == item.digest || existing.digest != item.digest {
						candidates[candidate.ID] = candidate
					}
				}
			}
			var proven []Item
			for _, candidate := range candidates {
				evidence := item
				// A proven move can rename the containing series directory. A
				// second present source or an existing different series stays isolated.
				if item.Kind == "episode" && !discovered[scanKey{candidate.rootKind, candidate.path}] && !knownSeries[item.SeriesID] {
					evidence.sourceSeriesID = candidate.sourceSeriesID
				}
				if candidate.digest != "" && candidate.digest == item.digest && !contradictoryIdentity(candidate, evidence) {
					proven = append(proven, candidate)
				}
			}
			if len(proven) == 1 {
				item.ID, item.AddedAt = proven[0].ID, proven[0].AddedAt
				item.SeriesID = proven[0].SeriesID
			} else {
				for candidateID, candidate := range candidates {
					if candidateID != item.ID && (candidate.digest == "" || candidate.digest == item.digest) {
						conflicts = append(conflicts, identityPair{item.Kind, "ambiguous_duplicate", candidateID, item.ID})
					}
				}
			}
			if samePath && contradictoryIdentity(old, item) {
				// Never attach a contradictory replacement through another candidate.
				item.ID = id(item.Kind, item.fingerprint+"\x00"+item.path+"\x00"+item.digest)
				conflicts = append(conflicts, identityPair{item.Kind, "replacement_evidence", old.ID, item.ID})
			}
		}
		sources[key] = item
		if primary, ok := next[item.ID]; !ok || key == primaries[item.ID] || (scanKey{primary.rootKind, primary.path} != primaries[item.ID] && item.path < primary.path) {
			next[item.ID] = item
		}
	}
	return next, sources, conflicts, nil
}

func persistIdentityConflicts(tx *sql.Tx, pairs []identityPair) error {
	for _, pair := range pairs {
		if pair.left == pair.right {
			continue
		}
		if pair.left > pair.right {
			pair.left, pair.right = pair.right, pair.left
		}
		_, err := tx.Exec(`INSERT INTO catalog_identity_conflicts(kind,reason,left_catalog_id,right_catalog_id,created_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, pair.kind, pair.reason, pair.left, pair.right, time.Now().UnixMilli())
		if err != nil {
			return err
		}
	}
	return nil
}
func detectProviderConflicts(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id,kind,metadata_provider,provider_id FROM catalog_items WHERE merged_into='' UNION ALL SELECT id,'series',metadata_provider,provider_id FROM catalog_series WHERE merged_into=''`)
	if err != nil {
		return err
	}
	seen := map[string][]string{}
	var pairs []identityPair
	for rows.Next() {
		var id, kind, provider, value string
		if err := rows.Scan(&id, &kind, &provider, &value); err != nil {
			rows.Close()
			return err
		}
		key := providerIdentity(provider, kind, value)
		if key == "" {
			continue
		}
		for _, other := range seen[key] {
			pairs = append(pairs, identityPair{kind, "provider_identity", other, id})
		}
		seen[key] = append(seen[key], id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return persistIdentityConflicts(tx, pairs)
}

func (c *Catalog) identityTarget(kind, id string) (Item, bool) {
	if kind == "episode" {
		item, ok := c.items[id]
		return publicMetadataItem(item), ok && item.Kind == kind
	}
	item, ok := c.metadataTarget(kind, id)
	return publicMetadataItem(item), ok
}

func (c *Catalog) IdentityRepairs() (IdentityRepairs, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := IdentityRepairs{Conflicts: []IdentityConflict{}, Merges: []IdentityMerge{}}
	if c.db == nil {
		return out, nil
	}
	rows, err := c.db.Query(`SELECT id,kind,reason,left_catalog_id,right_catalog_id,state FROM catalog_identity_conflicts WHERE state='open' ORDER BY id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v IdentityConflict
		var left, right string
		if err := rows.Scan(&v.ID, &v.Kind, &v.Reason, &left, &right, &v.State); err != nil {
			rows.Close()
			return out, err
		}
		v.Left, _ = c.identityTarget(v.Kind, left)
		v.Right, _ = c.identityTarget(v.Kind, right)
		out.Conflicts = append(out.Conflicts, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = c.db.Query(`SELECT id,kind,survivor_catalog_id,source_catalog_id,state,decisions_json FROM catalog_identity_merges ORDER BY created_at,id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v IdentityMerge
		var survivor, source, decisions string
		if err := rows.Scan(&v.ID, &v.Kind, &survivor, &source, &v.State, &decisions); err != nil {
			return out, err
		}
		v.Survivor, _ = c.identityTarget(v.Kind, survivor)
		v.Source, _ = c.identityTarget(v.Kind, source)
		if err := json.Unmarshal([]byte(decisions), &v.Decisions); err != nil {
			return out, err
		}
		out.Merges = append(out.Merges, v)
	}
	return out, rows.Err()
}
