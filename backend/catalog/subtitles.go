package catalog

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxSubtitleSidecarBytes = 8 << 20

func externalSubtitle(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt", ".vtt":
		return true
	}
	return false
}

func matchingSubtitleSidecars(mediaPath string, candidates []sidecarFile) []sidecarFile {
	dir := filepath.ToSlash(filepath.Dir(mediaPath))
	stem := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	prefix := stem + "."
	var matches []sidecarFile
	for _, candidate := range candidates {
		if filepath.ToSlash(filepath.Dir(candidate.rel)) != dir {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(candidate.rel), filepath.Ext(candidate.rel))
		suffix, ok := strings.CutPrefix(name, prefix)
		if !ok || !sidecarLanguage.MatchString(strings.SplitN(suffix, ".", 2)[0]) {
			continue
		}
		matches = append(matches, candidate)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].rel < matches[j].rel })
	return matches
}

func subtitleSidecarName(mediaPath, sidecarPath string) (language, title string, forced, sdh bool) {
	language, title = sidecarName(mediaPath, sidecarPath)
	parts := strings.Fields(strings.ToLower(title))
	kept := parts[:0]
	for _, part := range parts {
		switch part {
		case "forced":
			forced = true
		case "sdh", "cc":
			sdh = true
		default:
			kept = append(kept, part)
		}
	}
	if len(kept) == 0 {
		title = ""
	} else {
		title = strings.Join(kept, " ")
	}
	return
}

func hasExternalSubtitles(tracks []SubtitleTrack) bool {
	for _, track := range tracks {
		if track.External {
			return true
		}
	}
	return false
}

func embeddedSubtitles(tracks []SubtitleTrack) []SubtitleTrack {
	out := make([]SubtitleTrack, 0, len(tracks))
	for _, track := range tracks {
		if !track.External {
			out = append(out, track)
		}
	}
	return out
}

func externalSubtitleTracks(tracks []SubtitleTrack) []SubtitleTrack {
	var out []SubtitleTrack
	for _, track := range tracks {
		if track.External {
			out = append(out, track)
		}
	}
	return out
}

func nextSubtitleSelectionIndex(tracks []SubtitleTrack) int {
	next := 0
	for _, track := range tracks {
		if track.Index >= next {
			next = track.Index + 1
		}
	}
	return next
}

func (c *Catalog) appendSubtitleSidecars(ctx context.Context, root *os.Root, file scanFile, properties *MediaProperties) error {
	next := nextSubtitleSelectionIndex(properties.Subtitles)
	for _, sidecar := range file.subtitleSidecars {
		handle, err := root.Open(sidecar.rel)
		if err != nil {
			return fmt.Errorf("open external subtitle: %w", err)
		}
		info, err := handle.Stat()
		if err != nil {
			handle.Close()
			return fmt.Errorf("stat external subtitle: %w", err)
		}
		if info.Size() > maxSubtitleSidecarBytes {
			handle.Close()
			return fmt.Errorf("external subtitle exceeds %d bytes", maxSubtitleSidecarBytes)
		}
		inspected, probeErr := c.prober.Probe(ctx, handle)
		if probeErr != nil {
			handle.Close()
			return fmt.Errorf("probe external subtitle: %w", probeErr)
		}
		digest, digestErr := digestFile(ctx, handle)
		closeErr := handle.Close()
		if digestErr != nil {
			return fmt.Errorf("digest external subtitle: %w", digestErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close external subtitle: %w", closeErr)
		}
		language, title, forced, sdh := subtitleSidecarName(file.rel, sidecar.rel)
		for _, track := range inspected.Subtitles {
			if track.Index < 0 {
				continue
			}
			track.sourceIndex, track.Index = track.Index, next
			track.External, track.path = true, sidecar.rel
			track.size, track.mtime, track.digest, track.changeToken = info.Size(), info.ModTime().UnixNano(), digest, fileChangeToken(info)
			if unknownAudioLanguage(track.Language) {
				track.Language = language
			}
			if track.Title == "" {
				track.Title = title
			}
			track.Forced, track.SDH = track.Forced || forced, track.SDH || sdh
			properties.Subtitles = append(properties.Subtitles, track)
			next++
		}
	}
	return nil
}

func (c *Catalog) loadExternalSubtitles() error {
	rows, err := c.db.Query(`SELECT catalog_id,selection_index,source_stream_index,relative_path,codec,language,title,is_default,is_forced,is_sdh,size_bytes,mtime_unix,full_digest,change_token FROM catalog_subtitle_sidecars ORDER BY catalog_id,selection_index`)
	if err != nil {
		return fmt.Errorf("load external subtitles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var catalogID string
		var track SubtitleTrack
		var defaultValue, forced, sdh int
		if err := rows.Scan(&catalogID, &track.Index, &track.sourceIndex, &track.path, &track.Codec, &track.Language, &track.Title, &defaultValue, &forced, &sdh, &track.size, &track.mtime, &track.digest, &track.changeToken); err != nil {
			return fmt.Errorf("scan external subtitle: %w", err)
		}
		item, ok := c.items[catalogID]
		if !ok {
			continue
		}
		track.External, track.Default, track.Forced, track.SDH = true, defaultValue != 0, forced != 0, sdh != 0
		item.Subtitles = append(item.Subtitles, track)
		c.items[catalogID] = item
	}
	return rows.Err()
}

// SourceKey identifies one admitted subtitle source without revealing its path.
func (track SubtitleTrack) SourceKey() string {
	return id("subtitle", fmt.Sprintf("%d\x00%d\x00%t\x00%s\x00%s\x00%s\x00%d\x00%d", track.Index, track.sourceIndex, track.External, track.path, track.digest, track.changeToken, track.size, track.mtime))
}

// SourceStreamIndex is the selected FFmpeg input-local stream index.
func (track SubtitleTrack) SourceStreamIndex() int {
	if track.External {
		return track.sourceIndex
	}
	return track.Index
}

// OpenSubtitleSource reopens only the independently pinned, root-confined source.
func (c *Catalog) OpenSubtitleSource(id, videoSourceKey, subtitleSourceKey string, selectionIndex int, external bool) (*os.File, error) {
	if !external {
		return c.OpenSource(id, videoSourceKey)
	}
	c.mu.RLock()
	item, ok := c.playbackSource(id)
	rootPath := c.film
	if item.rootKind == "episode" {
		rootPath = c.tv
	}
	var selected SubtitleTrack
	for _, track := range item.Subtitles {
		if track.External && track.Index == selectionIndex {
			selected = track
			break
		}
	}
	item.sourceRoot = rootPath
	c.mu.RUnlock()
	if !ok || !item.Playable || item.SourceKey() != videoSourceKey || selected.path == "" || selected.SourceKey() != subtitleSourceKey {
		return nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(selected.path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() != selected.size || info.ModTime().UnixNano() != selected.mtime || fileChangeToken(info) != selected.changeToken {
		file.Close()
		return nil, os.ErrNotExist
	}
	digest, err := digestFile(context.Background(), file)
	if err != nil || digest != selected.digest {
		file.Close()
		return nil, os.ErrNotExist
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
