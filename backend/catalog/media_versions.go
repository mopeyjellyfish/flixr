package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/access"
)

var (
	ErrMediaVersionConflict    = errors.New("media version group conflicts with existing catalog state")
	ErrMediaVersionUnavailable = errors.New("media version is unavailable")
)

type MediaVersion struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	EditionID    string `json:"edition_id"`
	EditionLabel string `json:"edition_label,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	HDR          string `json:"hdr,omitempty"`
	VideoCodec   string `json:"video_codec,omitempty"`
	Container    string `json:"container,omitempty"`
	Bitrate      int64  `json:"bitrate,omitempty"`
	Selected     bool   `json:"selected"`
	Available    bool   `json:"available"`
}

type MediaVersionGroup struct {
	ID             string         `json:"id"`
	LogicalTitleID string         `json:"logical_title_id"`
	Title          string         `json:"title"`
	Kind           string         `json:"kind"`
	EditionID      string         `json:"edition_id"`
	EditionLabel   string         `json:"edition_label,omitempty"`
	Members        []MediaVersion `json:"members"`
}

type MediaVersionCandidate struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Kind         string `json:"kind"`
	EditionID    string `json:"edition_id"`
	EditionLabel string `json:"edition_label,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	HDR          string `json:"hdr,omitempty"`
	VideoCodec   string `json:"video_codec,omitempty"`
	Container    string `json:"container,omitempty"`
	Bitrate      int64  `json:"bitrate,omitempty"`
}

type MediaVersionGroups struct {
	Groups     []MediaVersionGroup     `json:"groups"`
	Candidates []MediaVersionCandidate `json:"candidates"`
	Total      int                     `json:"total"`
	NextOffset *int                    `json:"next_offset,omitempty"`
}

// PlaybackVersion is one authorized physical source for a logical history item.
type PlaybackVersion struct {
	Item     Item
	Version  MediaVersion
	Explicit bool
}

func versionLabel(item Item) string {
	label := strings.TrimSpace(fmt.Sprintf("%dp", item.Height))
	if item.Height == 0 {
		label = strings.ToUpper(item.VideoCodec)
	}
	if label == "" {
		label = "Original"
	}
	if item.HDR != "" {
		label += " HDR"
	}
	return label
}

func mediaVersion(id, editionID, editionLabel string, item Item, available bool) MediaVersion {
	return MediaVersion{ID: id, Label: versionLabel(item), EditionID: editionID, EditionLabel: editionLabel, Width: item.Width, Height: item.Height, HDR: item.HDR, VideoCodec: item.VideoCodec, Container: item.Container, Bitrate: item.Bitrate, Available: available}
}

func (c *Catalog) editionLabel(kind, id string) string {
	if c.db == nil {
		return ""
	}
	var label string
	_ = c.db.QueryRow(`SELECT label FROM catalog_edition_labels WHERE kind=? AND catalog_id=?`, kind, id).Scan(&label)
	return label
}

func membershipTable(kind string) (table, canonical, member string, ok bool) {
	switch kind {
	case "film":
		return "catalog_film_version_memberships", "canonical_catalog_id", "member_catalog_id", true
	case "series":
		return "catalog_series_version_memberships", "canonical_series_id", "member_series_id", true
	default:
		return "", "", "", false
	}
}

func (c *Catalog) canonicalVersionID(kind, id string) (string, error) {
	table, canonical, member, ok := membershipTable(kind)
	if !ok {
		return "", ErrCatalogNotFound
	}
	if c.db == nil {
		return id, nil
	}
	var found string
	err := c.db.QueryRow(`SELECT `+canonical+` FROM `+table+` WHERE `+member+`=?`, id).Scan(&found)
	if err == nil {
		return found, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return id, nil
}

func (c *Catalog) IsMediaVersionMember(kind, id string) bool {
	if c.db == nil {
		return false
	}
	if kind == "episode" {
		item, ok := c.Item(id)
		if !ok {
			return false
		}
		kind, id = "series", item.SeriesID
	}
	canonical, err := c.canonicalVersionID(kind, id)
	return err == nil && canonical != id
}

func (c *Catalog) groupMemberIDs(kind, canonicalID string) ([]string, error) {
	table, canonical, member, ok := membershipTable(kind)
	if !ok {
		return nil, ErrCatalogNotFound
	}
	if c.db == nil {
		return []string{canonicalID}, nil
	}
	rows, err := c.db.Query(`SELECT `+member+` FROM `+table+` WHERE `+canonical+`=? ORDER BY `+member, canonicalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{canonicalID}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (c *Catalog) anchorItem(kind, id string) (Item, bool) {
	if kind == "film" {
		x, ok := c.Item(id)
		return x, ok && x.Kind == "film"
	}
	series, ok := c.Series(id)
	if !ok {
		return Item{}, false
	}
	for _, season := range series.Seasons {
		if len(season.Episodes) > 0 {
			x := season.Episodes[0]
			x.ID, x.Title, x.Kind = series.ID, series.Title, "series"
			return x, true
		}
	}
	return Item{ID: series.ID, Title: series.Title, Kind: "series", Playable: series.Playable}, true
}

func (c *Catalog) groupObject(kind, canonicalID string) (MediaVersionGroup, error) {
	anchor, ok := c.anchorItem(kind, canonicalID)
	if !ok {
		return MediaVersionGroup{}, ErrCatalogNotFound
	}
	ids, err := c.groupMemberIDs(kind, canonicalID)
	if err != nil {
		return MediaVersionGroup{}, err
	}
	label := c.editionLabel(kind, canonicalID)
	group := MediaVersionGroup{ID: canonicalID, LogicalTitleID: canonicalID, Title: anchor.Title, Kind: kind, EditionID: canonicalID, EditionLabel: label, Members: []MediaVersion{}}
	for _, id := range ids {
		x, found := c.anchorItem(kind, id)
		if !found {
			continue
		}
		group.Members = append(group.Members, mediaVersion(id, canonicalID, label, x, x.Playable))
	}
	return group, nil
}

func (c *Catalog) MediaVersionGroups() (MediaVersionGroups, error) {
	return c.MediaVersionGroupsPage("", 0, 0)
}

func (c *Catalog) MediaVersionGroupsPage(query string, offset, limit int) (MediaVersionGroups, error) {
	out := MediaVersionGroups{Groups: []MediaVersionGroup{}, Candidates: []MediaVersionCandidate{}}
	if c.db == nil {
		return out, nil
	}
	items, total, err := c.Browse(query, offset, limit)
	if err != nil {
		return out, err
	}
	out.Total = total
	if limit > 0 && offset+len(items) < total {
		next := offset + len(items)
		out.NextOffset = &next
	}
	for _, x := range items {
		group, groupErr := c.groupObject(x.Kind, x.ID)
		if groupErr != nil {
			return out, groupErr
		}
		out.Groups = append(out.Groups, group)
		anchor, ok := c.anchorItem(x.Kind, x.ID)
		if !ok {
			continue
		}
		out.Candidates = append(out.Candidates, MediaVersionCandidate{ID: x.ID, Title: x.Title, Kind: x.Kind, EditionID: x.ID, EditionLabel: c.editionLabel(x.Kind, x.ID), Width: anchor.Width, Height: anchor.Height, HDR: anchor.HDR, VideoCodec: anchor.VideoCodec, Container: anchor.Container, Bitrate: anchor.Bitrate})
	}
	return out, nil
}

func (c *Catalog) validateSeriesCoordinates(ids []string) error {
	var expected map[string]bool
	for _, id := range ids {
		series, ok := c.Series(id)
		if !ok {
			return ErrCatalogNotFound
		}
		coords := map[string]bool{}
		for _, season := range series.Seasons {
			for _, episode := range season.Episodes {
				key := fmt.Sprintf("%d/%d", episode.Season, episode.Episode)
				if episode.Episode <= 0 || coords[key] {
					return ErrMediaVersionConflict
				}
				coords[key] = true
			}
		}
		if expected == nil {
			expected = coords
			continue
		}
		if len(coords) != len(expected) {
			return ErrMediaVersionConflict
		}
		for key := range expected {
			if !coords[key] {
				return ErrMediaVersionConflict
			}
		}
	}
	return nil
}

func (c *Catalog) CreateMediaVersionGroup(ctx context.Context, kind, canonicalID string, memberIDs []string) (MediaVersionGroup, error) {
	c.mediaVersionsMu.Lock()
	defer c.mediaVersionsMu.Unlock()
	if c.db == nil || canonicalID == "" || len(memberIDs) == 0 {
		return MediaVersionGroup{}, ErrMediaVersionConflict
	}
	table, canonical, member, ok := membershipTable(kind)
	if !ok {
		return MediaVersionGroup{}, ErrMediaVersionConflict
	}
	ids, seen := []string{canonicalID}, map[string]bool{canonicalID: true}
	for _, id := range memberIDs {
		if id == "" || seen[id] {
			return MediaVersionGroup{}, ErrMediaVersionConflict
		}
		seen[id], ids = true, append(ids, id)
	}
	for _, id := range ids {
		if _, ok := c.anchorItem(kind, id); !ok {
			return MediaVersionGroup{}, ErrCatalogNotFound
		}
		resolved, err := c.canonicalVersionID(kind, id)
		if err != nil || resolved != id {
			return MediaVersionGroup{}, ErrMediaVersionConflict
		}
		var count int
		if err := c.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+canonical+`=?`, id).Scan(&count); err != nil || count != 0 {
			return MediaVersionGroup{}, ErrMediaVersionConflict
		}
	}
	if kind == "series" {
		if err := c.validateSeriesCoordinates(ids); err != nil {
			return MediaVersionGroup{}, err
		}
	}
	canonicalLabel := c.editionLabel(kind, canonicalID)
	for _, id := range memberIDs {
		if label := c.editionLabel(kind, id); label != "" && label != canonicalLabel {
			return MediaVersionGroup{}, ErrMediaVersionConflict
		}
	}
	tx, err := c.db.BeginTx(ctx)
	if err != nil {
		return MediaVersionGroup{}, err
	}
	defer tx.Rollback()
	for _, id := range memberIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+`(`+canonical+`,`+member+`,created_at) VALUES(?,?,?)`, canonicalID, id, time.Now().Unix()); err != nil {
			return MediaVersionGroup{}, ErrMediaVersionConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return MediaVersionGroup{}, err
	}
	return c.groupObject(kind, canonicalID)
}

func (c *Catalog) SetEditionLabel(ctx context.Context, kind, id, label string) (MediaVersionGroup, error) {
	c.mediaVersionsMu.Lock()
	defer c.mediaVersionsMu.Unlock()
	if _, _, _, ok := membershipTable(kind); !ok || len(strings.TrimSpace(label)) > 80 {
		return MediaVersionGroup{}, ErrMediaVersionConflict
	}
	if _, ok := c.anchorItem(kind, id); !ok {
		return MediaVersionGroup{}, ErrCatalogNotFound
	}
	canonicalID, err := c.canonicalVersionID(kind, id)
	if err != nil || canonicalID != id {
		return MediaVersionGroup{}, ErrMediaVersionConflict
	}
	label = strings.TrimSpace(label)
	if _, err := c.db.Exec(`INSERT INTO catalog_edition_labels(kind,catalog_id,label,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,catalog_id) DO UPDATE SET label=excluded.label,updated_at=excluded.updated_at`, kind, id, label, time.Now().Unix()); err != nil {
		return MediaVersionGroup{}, err
	}
	return c.groupObject(kind, id)
}

func (c *Catalog) UngroupMediaVersion(ctx context.Context, kind, canonicalID, memberID string) (MediaVersionGroup, error) {
	c.mediaVersionsMu.Lock()
	defer c.mediaVersionsMu.Unlock()
	table, canonical, member, ok := membershipTable(kind)
	if !ok || canonicalID == memberID {
		return MediaVersionGroup{}, ErrMediaVersionConflict
	}
	result, err := c.db.Exec(`DELETE FROM `+table+` WHERE `+canonical+`=? AND `+member+`=?`, canonicalID, memberID)
	if err != nil {
		return MediaVersionGroup{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return MediaVersionGroup{}, ErrCatalogNotFound
	}
	return c.groupObject(kind, canonicalID)
}

func (c *Catalog) episodeForSeries(seriesID string, season, episode int) (Item, bool) {
	series, ok := c.Series(seriesID)
	if !ok {
		return Item{}, false
	}
	for _, s := range series.Seasons {
		for _, x := range s.Episodes {
			if x.Season == season && x.Episode == episode {
				return x, true
			}
		}
	}
	return Item{}, false
}

func (c *Catalog) versionAnchors(item Item) (kind, canonicalID string, anchors []string, err error) {
	kind, canonicalID = "film", item.ID
	if item.Kind == "episode" {
		kind, canonicalID = "series", item.SeriesID
	}
	canonicalID, err = c.canonicalVersionID(kind, canonicalID)
	if err != nil {
		return "", "", nil, err
	}
	anchors, err = c.groupMemberIDs(kind, canonicalID)
	return
}

// PlaybackVersions returns only sources allowed by the profile's library policy.
func (c *Catalog) PlaybackVersions(ctx context.Context, profileID, catalogID string, policy access.Policy) ([]PlaybackVersion, error) {
	logical, ok := c.Item(catalogID)
	if !ok || (logical.Kind != "film" && logical.Kind != "episode") {
		return nil, ErrCatalogNotFound
	}
	if c.db == nil {
		versionID := logical.ID
		if logical.Kind == "episode" {
			versionID = logical.SeriesID
		}
		return []PlaybackVersion{{Item: logical, Version: mediaVersion(versionID, versionID, "", logical, logical.Playable)}}, nil
	}
	kind, canonicalID, anchors, err := c.versionAnchors(logical)
	if err != nil {
		return nil, err
	}
	preference := ""
	_ = c.db.QueryRow(`SELECT version_id FROM profile_media_version_preferences WHERE profile_id=? AND kind=? AND catalog_id=?`, profileID, kind, canonicalID).Scan(&preference)
	editionLabel := c.editionLabel(kind, canonicalID)
	out := []PlaybackVersion{}
	for _, anchorID := range anchors {
		sourceID := anchorID
		if kind == "series" {
			x, found := c.episodeForSeries(anchorID, logical.Season, logical.Episode)
			if !found {
				continue
			}
			sourceID = x.ID
		}
		sources, err := c.physicalSources(sourceID)
		if err != nil {
			continue
		}
		legacySource := len(sources) == 0
		if legacySource {
			if legacy, legacyErr := c.PlaybackItemContext(ctx, sourceID); legacyErr == nil {
				sources = []physicalSource{{Item: legacy}}
			} else if unavailable, found := c.Item(sourceID); found {
				sources = []physicalSource{{Item: unavailable}}
			}
		}
		for _, source := range sources {
			if policy.Restricted() {
				content, found, err := c.AccessContentForItem(sourceID)
				if err != nil || !found || !policy.Allows(content) {
					continue
				}
				if len(policy.LibraryIDs) > 0 && !containsString(policy.LibraryIDs, source.libraryID) {
					continue
				}
			}
			available := source.Playable
			if available && !legacySource {
				file, openErr := c.openSourceItem(source.Item)
				if openErr == nil {
					file.Close()
				} else {
					available = false
				}
			}
			v := mediaVersion(anchorID, canonicalID, editionLabel, source.Item, available)
			v.Selected = anchorID == preference
			out = append(out, PlaybackVersion{Item: source.Item, Version: v})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Version.ID == out[j].Version.ID {
			return out[i].Item.Bitrate > out[j].Item.Bitrate
		}
		return out[i].Version.ID < out[j].Version.ID
	})
	return out, nil
}

func (c *Catalog) MediaVersions(ctx context.Context, profileID, catalogID string, policy access.Policy) ([]MediaVersion, error) {
	choices, err := c.PlaybackVersions(ctx, profileID, catalogID, policy)
	if err != nil {
		return nil, err
	}
	out, indexes := []MediaVersion{}, map[string]int{}
	for _, choice := range choices {
		if index, ok := indexes[choice.Version.ID]; ok {
			out[index].Available = out[index].Available || choice.Version.Available
			continue
		}
		indexes[choice.Version.ID] = len(out)
		out = append(out, choice.Version)
	}
	return out, nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (c *Catalog) SavePlaybackVersion(profileID, catalogID, versionID string) error {
	if c.db == nil {
		return nil
	}
	logical, ok := c.Item(catalogID)
	if !ok {
		return ErrCatalogNotFound
	}
	kind, canonicalID, _, err := c.versionAnchors(logical)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`INSERT INTO profile_media_version_preferences(profile_id,kind,catalog_id,version_id,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(profile_id,kind,catalog_id) DO UPDATE SET version_id=excluded.version_id,updated_at=excluded.updated_at`, profileID, kind, canonicalID, versionID, time.Now().Unix())
	return err
}

func (c *Catalog) PlaybackVersionPreference(profileID, catalogID string) (string, error) {
	if c.db == nil {
		return "", nil
	}
	logical, ok := c.Item(catalogID)
	if !ok {
		return "", ErrCatalogNotFound
	}
	kind, canonicalID, _, err := c.versionAnchors(logical)
	if err != nil {
		return "", err
	}
	var versionID string
	err = c.db.QueryRow(`SELECT version_id FROM profile_media_version_preferences WHERE profile_id=? AND kind=? AND catalog_id=?`, profileID, kind, canonicalID).Scan(&versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return versionID, err
}

func (c *Catalog) PlaybackVersionSource(ctx context.Context, profileID, catalogID, versionID, sourceKey string, policy access.Policy) (Item, error) {
	choices, err := c.PlaybackVersions(ctx, profileID, catalogID, policy)
	if err != nil {
		return Item{}, err
	}
	for _, choice := range choices {
		if choice.Version.ID == versionID && choice.Item.SourceKey() == sourceKey && choice.Version.Available {
			return choice.Item, nil
		}
	}
	return Item{}, ErrMediaVersionUnavailable
}

func (c *Catalog) groupedPhysicalSources(catalogID string) ([]physicalSource, error) {
	item, ok := c.Item(catalogID)
	if !ok {
		return nil, ErrCatalogNotFound
	}
	_, _, anchors, err := c.versionAnchors(item)
	if err != nil {
		return nil, err
	}
	out := []physicalSource{}
	for _, anchor := range anchors {
		sourceID := anchor
		if item.Kind == "episode" {
			mapped, found := c.episodeForSeries(anchor, item.Season, item.Episode)
			if !found {
				continue
			}
			sourceID = mapped.ID
		}
		sources, sourceErr := c.physicalSources(sourceID)
		if sourceErr == nil {
			out = append(out, sources...)
		}
	}
	return out, nil
}

func (c *Catalog) MediaVersionPlaybackIDs(kind string, anchorIDs ...string) []string {
	if kind == "film" {
		return append([]string(nil), anchorIDs...)
	}
	seen, out := map[string]bool{}, []string{}
	for _, seriesID := range anchorIDs {
		series, ok := c.Series(seriesID)
		if !ok {
			continue
		}
		for _, season := range series.Seasons {
			for _, episode := range season.Episodes {
				if !seen[episode.ID] {
					seen[episode.ID] = true
					out = append(out, episode.ID)
				}
			}
		}
	}
	return out
}
