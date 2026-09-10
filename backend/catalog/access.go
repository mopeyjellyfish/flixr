package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/access"
)

var ErrAccessDenied = errors.New("catalog access denied")

// AccessContent returns the policy inputs for a logical title. Library IDs are
// derived from every present physical source, rather than the current primary.
func (c *Catalog) AccessContent(kind, id string) (access.Content, bool, error) {
	if id == "" {
		return access.Content{}, false, nil
	}
	c.mu.RLock()
	var item Item
	var ok bool
	metadataKind, metadataID := kind, id
	switch kind {
	case "film", "episode":
		item, ok = c.items[id]
		if ok && item.Kind != kind {
			ok = false
		}
		if ok && kind == "episode" {
			metadataKind, metadataID = "series", item.SeriesID
		}
	case "series":
		series, found := c.series[id]
		if found {
			item = Item{ID: series.ID, Kind: "series", Genres: append([]string(nil), series.Genres...)}
		}
		ok = found
	default:
		c.mu.RUnlock()
		return access.Content{}, false, nil
	}
	c.mu.RUnlock()
	if !ok {
		return access.Content{}, false, nil
	}
	content := access.Content{Tags: append([]string(nil), item.Genres...)}
	if c.db == nil {
		if kind == "film" {
			content.LibraryIDs = []string{"films"}
		} else {
			content.LibraryIDs = []string{"tv"}
		}
		return content, true, nil
	}
	query := `SELECT DISTINCT x.library_id FROM catalog_physical_files f JOIN library_locations x ON x.id=f.location_id WHERE f.catalog_id=? AND f.present=1 ORDER BY x.library_id`
	if kind == "series" {
		query = `SELECT DISTINCT x.library_id FROM catalog_items i JOIN catalog_physical_files f ON f.catalog_id=i.id JOIN library_locations x ON x.id=f.location_id WHERE i.series_id=? AND i.merged_into='' AND f.present=1 ORDER BY x.library_id`
	}
	rows, err := c.db.Query(query, id)
	if err != nil {
		return access.Content{}, false, fmt.Errorf("load catalog access libraries: %w", err)
	}
	for rows.Next() {
		var libraryID string
		if err := rows.Scan(&libraryID); err != nil {
			rows.Close()
			return access.Content{}, false, fmt.Errorf("scan catalog access library: %w", err)
		}
		content.LibraryIDs = append(content.LibraryIDs, libraryID)
	}
	if err := rows.Close(); err != nil {
		return access.Content{}, false, fmt.Errorf("close catalog access libraries: %w", err)
	}
	if metadataID == "" {
		return content, true, nil
	}
	fields, err := c.db.Query(`SELECT field,value FROM catalog_metadata_fields WHERE catalog_kind=? AND catalog_id=? AND field IN ('tags','content_rating')`, metadataKind, metadataID)
	if err != nil {
		return access.Content{}, false, fmt.Errorf("load catalog access metadata: %w", err)
	}
	defer fields.Close()
	for fields.Next() {
		var field, value string
		if err := fields.Scan(&field, &value); err != nil {
			return access.Content{}, false, fmt.Errorf("scan catalog access metadata: %w", err)
		}
		switch field {
		case "content_rating":
			content.Rating = strings.TrimSpace(value)
		case "tags":
			for _, tag := range strings.Split(value, ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					content.Tags = append(content.Tags, tag)
				}
			}
		}
	}
	return content, true, fields.Err()
}

func (c *Catalog) PlaybackItemForPolicy(ctx context.Context, id string, policy access.Policy) (Item, error) {
	if !policy.Restricted() {
		return c.PlaybackItemContext(ctx, id)
	}
	content, ok, err := c.AccessContentForItem(id)
	if err != nil {
		return Item{}, err
	}
	if !ok || !policy.Allows(content) {
		return Item{}, ErrAccessDenied
	}
	if len(policy.LibraryIDs) == 0 {
		return c.PlaybackItemContext(ctx, id)
	}
	allowed := make(map[string]bool, len(policy.LibraryIDs))
	for _, libraryID := range policy.LibraryIDs {
		allowed[libraryID] = true
	}
	candidates, err := c.physicalSources(id)
	if err != nil {
		return Item{}, err
	}
	for _, candidate := range candidates {
		if !allowed[candidate.libraryID] {
			continue
		}
		item := candidate.Item
		if !item.Playable || item.digest == "" {
			continue
		}
		file, err := c.openSourceItem(item)
		if err != nil {
			continue
		}
		file.Close()
		item.Audio = withoutExternalAudio(item.Audio)
		item.Subtitles = withoutExternalSubtitles(item.Subtitles)
		return item, nil
	}
	return Item{}, ErrAccessDenied
}

func (c *Catalog) AccessContentForItem(id string) (access.Content, bool, error) {
	c.mu.RLock()
	item, ok := c.items[id]
	c.mu.RUnlock()
	if !ok {
		return access.Content{}, false, nil
	}
	return c.AccessContent(item.Kind, id)
}

type physicalSource struct {
	Item
	libraryID string
	selected  bool
}

func (c *Catalog) physicalSources(id string) ([]physicalSource, error) {
	if c.db == nil {
		return nil, nil
	}
	c.mu.RLock()
	base, ok := c.playbackSource(id)
	c.mu.RUnlock()
	if !ok {
		return nil, ErrCatalogNotFound
	}
	rows, err := c.db.Query(`SELECT f.location_id,x.library_id,x.root_path,f.root_kind,f.relative_path,f.full_digest,f.change_token,f.source_series_id,f.size_bytes,f.mtime_unix,f.selected,f.container,f.duration_ms,f.video_codec,f.video_profile,f.video_level,f.primary_video_stream_index,f.video_width,f.video_height,f.video_bitrate,f.video_frame_rate_milli,f.video_bit_depth,f.video_hdr,f.audio_json,f.subtitle_json,f.probe_revision FROM catalog_physical_files f JOIN library_locations x ON x.id=f.location_id WHERE f.catalog_id=? AND f.present=1`, id)
	if err != nil {
		return nil, fmt.Errorf("load physical access sources: %w", err)
	}
	defer rows.Close()
	var out []physicalSource
	for rows.Next() {
		x := physicalSource{Item: base}
		var selected int
		var audio, subtitles string
		if err := rows.Scan(&x.sourceLocationID, &x.libraryID, &x.sourceRoot, &x.rootKind, &x.path, &x.digest, &x.changeToken, &x.sourceSeriesID, &x.size, &x.mtime, &selected, &x.Container, &x.DurationMS, &x.VideoCodec, &x.VideoProfile, &x.VideoLevel, &x.PrimaryVideoStreamIndex, &x.Width, &x.Height, &x.Bitrate, &x.FrameRateMilli, &x.BitDepth, &x.HDR, &audio, &subtitles, &x.probeRevision); err != nil {
			return nil, fmt.Errorf("scan physical access source: %w", err)
		}
		embeddedAudio, embeddedSubtitles := []AudioTrack{}, []SubtitleTrack{}
		if json.Unmarshal([]byte(audio), &embeddedAudio) != nil || json.Unmarshal([]byte(subtitles), &embeddedSubtitles) != nil {
			return nil, errors.New("decode physical media properties")
		}
		x.Audio = append(embeddedAudio, externalAudioTracks(base.Audio)...)
		x.Subtitles = append(embeddedSubtitles, externalSubtitleTracks(base.Subtitles)...)
		x.selected = selected != 0
		out = append(out, x)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].selected && !out[j].selected })
	return out, rows.Err()
}

func withoutExternalAudio(tracks []AudioTrack) []AudioTrack {
	out := tracks[:0:0]
	for _, track := range tracks {
		if !track.External {
			out = append(out, track)
		}
	}
	return out
}

func withoutExternalSubtitles(tracks []SubtitleTrack) []SubtitleTrack {
	out := tracks[:0:0]
	for _, track := range tracks {
		if !track.External {
			out = append(out, track)
		}
	}
	return out
}
