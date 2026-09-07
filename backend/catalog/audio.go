package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var sidecarLanguage = regexp.MustCompile(`(?i)^[a-z]{2,3}(?:-[a-z0-9]{2,8})*$`)

func unknownAudioLanguage(language string) bool {
	language = strings.TrimSpace(language)
	return language == "" || strings.EqualFold(language, "und") || strings.EqualFold(language, "unknown")
}

func externalAudio(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".flac", ".m4a", ".mp3", ".ogg", ".opus", ".wav":
		return true
	}
	return false
}

func matchingAudioSidecars(mediaPath string, candidates []sidecarFile) []sidecarFile {
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
		if !ok {
			continue
		}
		language := strings.SplitN(suffix, ".", 2)[0]
		if sidecarLanguage.MatchString(language) {
			matches = append(matches, candidate)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].rel < matches[j].rel })
	return matches
}

func sidecarName(mediaPath, sidecarPath string) (language, title string) {
	stem := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	name := strings.TrimSuffix(filepath.Base(sidecarPath), filepath.Ext(sidecarPath))
	suffix := strings.TrimPrefix(name, stem+".")
	parts := strings.SplitN(suffix, ".", 2)
	language = strings.ToLower(parts[0])
	if len(parts) == 2 {
		title = strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ").Replace(parts[1]))
	}
	return language, title
}

func hasExternalAudio(tracks []AudioTrack) bool {
	for _, track := range tracks {
		if track.External {
			return true
		}
	}
	return false
}

func embeddedAudio(tracks []AudioTrack) []AudioTrack {
	out := make([]AudioTrack, 0, len(tracks))
	for _, track := range tracks {
		if !track.External {
			out = append(out, track)
		}
	}
	return out
}

func externalAudioTracks(tracks []AudioTrack) []AudioTrack {
	var out []AudioTrack
	for _, track := range tracks {
		if track.External {
			out = append(out, track)
		}
	}
	return out
}

func nextAudioSelectionIndex(tracks []AudioTrack) int {
	next := 0
	for _, track := range tracks {
		if track.Index >= next {
			next = track.Index + 1
		}
	}
	return next
}

func (c *Catalog) appendAudioSidecars(ctx context.Context, root *os.Root, file scanFile, properties *MediaProperties) error {
	next := nextAudioSelectionIndex(properties.Audio)
	for _, sidecar := range file.sidecars {
		handle, err := root.Open(sidecar.rel)
		if err != nil {
			return fmt.Errorf("open external audio: %w", err)
		}
		inspected, probeErr := c.prober.Probe(ctx, handle)
		closeErr := handle.Close()
		if probeErr != nil {
			return fmt.Errorf("probe external audio: %w", probeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close external audio: %w", closeErr)
		}
		language, title := sidecarName(file.rel, sidecar.rel)
		for _, track := range inspected.Audio {
			if track.Index < 0 {
				continue
			}
			track.sourceIndex = track.Index
			track.Index = next
			track.External = true
			track.path = sidecar.rel
			if unknownAudioLanguage(track.Language) {
				track.Language = language
			}
			if track.Title == "" {
				track.Title = title
			}
			properties.Audio = append(properties.Audio, track)
			next++
		}
	}
	return nil
}

func (c *Catalog) loadExternalAudio() error {
	rows, err := c.db.Query(`SELECT catalog_id,selection_index,source_stream_index,relative_path,codec,channels,language,title,is_default,is_forced FROM catalog_audio_sidecars ORDER BY catalog_id,selection_index`)
	if err != nil {
		return fmt.Errorf("load external audio: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var catalogID string
		var track AudioTrack
		var defaultValue, forced int
		if err := rows.Scan(&catalogID, &track.Index, &track.sourceIndex, &track.path, &track.Codec, &track.Channels, &track.Language, &track.Title, &defaultValue, &forced); err != nil {
			return fmt.Errorf("scan external audio: %w", err)
		}
		item, ok := c.items[catalogID]
		if !ok {
			continue
		}
		track.External, track.Default, track.Forced = true, defaultValue != 0, forced != 0
		item.Audio = append(item.Audio, track)
		c.items[catalogID] = item
	}
	return rows.Err()
}

// OpenAudio opens a catalog-validated external audio selection beneath its media root.
func (c *Catalog) OpenAudio(id string, selectionIndex int, external bool) (*os.File, error) {
	if !external {
		return c.Open(id)
	}
	c.mu.RLock()
	item, ok := c.items[id]
	rootPath := c.film
	if item.rootKind == "episode" {
		rootPath = c.tv
	}
	var relativePath string
	for _, track := range item.Audio {
		if track.External && track.Index == selectionIndex {
			relativePath = track.path
			break
		}
	}
	c.mu.RUnlock()
	if !ok || relativePath == "" {
		return nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(relativePath)
}

// SourceStreamIndex is the FFmpeg input-local index recorded by ffprobe.
func (track AudioTrack) SourceStreamIndex() int {
	if track.External {
		return track.sourceIndex
	}
	return track.Index
}
