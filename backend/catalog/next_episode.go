package catalog

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type EpisodeSequenceState string

const (
	EpisodeSequenceNext               EpisodeSequenceState = "next"
	EpisodeSequenceEnd                EpisodeSequenceState = "end_of_series"
	EpisodeSequenceNotEpisodic        EpisodeSequenceState = "not_episodic"
	EpisodeSequenceContextUnavailable EpisodeSequenceState = "context_unavailable"
)

type EpisodeSequence struct {
	State   EpisodeSequenceState `json:"state"`
	Episode *Item                `json:"episode,omitempty"`
}

// EpisodeAfter returns the next playable, incomplete episode after current in
// numeric order. The private sequence context keeps advancement within the
// current root and edition/version directory.
func (c *Catalog) EpisodeAfter(profileID, currentID string, includeSpecials bool) (EpisodeSequence, error) {
	return c.episodeAfter(profileID, currentID, includeSpecials, nil)
}

// EpisodeAfterAllowed applies profile visibility before calculating sequence
// state, so a hidden episode cannot appear in Next Up or affect its counts.
func (c *Catalog) EpisodeAfterAllowed(profileID, currentID string, includeSpecials bool, allowed func(Item) (bool, error)) (EpisodeSequence, error) {
	return c.episodeAfter(profileID, currentID, includeSpecials, allowed)
}

func (c *Catalog) episodeAfter(profileID, currentID string, includeSpecials bool, allowed func(Item) (bool, error)) (EpisodeSequence, error) {
	c.mu.RLock()
	current, ok := c.items[currentID]
	items := make([]Item, 0, len(c.items))
	for _, item := range c.items {
		items = append(items, item)
	}
	c.mu.RUnlock()
	if !ok {
		return EpisodeSequence{}, ErrCatalogNotFound
	}
	if current.Kind != "episode" {
		return EpisodeSequence{State: EpisodeSequenceNotEpisodic}, nil
	}
	if current.Season < 0 || current.Episode <= 0 || (current.Season == 0 && !includeSpecials) {
		return EpisodeSequence{State: EpisodeSequenceContextUnavailable}, nil
	}

	completed, err := c.completedEpisodes(profileID, current.SeriesID)
	if err != nil {
		return EpisodeSequence{}, err
	}
	contextKey := episodeSequenceContext(current)
	hasLaterEpisode := false
	hasContextEpisode := false
	candidates := make([]Item, 0)
	for _, item := range items {
		if item.ID == current.ID || item.Kind != "episode" || item.SeriesID != current.SeriesID || !item.Playable || item.Episode <= 0 {
			continue
		}
		if item.Season < 0 || (item.Season == 0 && !includeSpecials) || compareEpisode(item, current) <= 0 {
			continue
		}
		if allowed != nil {
			visible, err := allowed(item)
			if err != nil {
				return EpisodeSequence{}, err
			}
			if !visible {
				continue
			}
		}
		hasLaterEpisode = true
		if episodeSequenceContext(item) != contextKey {
			continue
		}
		hasContextEpisode = true
		if !completed[item.ID] {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		if hasLaterEpisode && !hasContextEpisode {
			return EpisodeSequence{State: EpisodeSequenceContextUnavailable}, nil
		}
		return EpisodeSequence{State: EpisodeSequenceEnd}, nil
	}
	slices.SortFunc(candidates, func(a, b Item) int {
		return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode), cmp.Compare(a.ID, b.ID))
	})
	if len(candidates) > 1 && candidates[0].Season == candidates[1].Season && candidates[0].Episode == candidates[1].Episode {
		return EpisodeSequence{State: EpisodeSequenceContextUnavailable}, nil
	}
	next := candidates[0]
	return EpisodeSequence{State: EpisodeSequenceNext, Episode: &next}, nil
}

func (c *Catalog) completedEpisodes(profileID, seriesID string) (map[string]bool, error) {
	completed := map[string]bool{}
	if c.db == nil {
		return completed, nil
	}
	rows, err := c.db.Query(`SELECT p.catalog_id FROM progress p JOIN catalog_items i ON i.id=p.catalog_id WHERE p.profile_id=? AND p.completed=1 AND i.series_id=?`, profileID, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list completed episodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan completed episode: %w", err)
		}
		completed[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate completed episodes: %w", err)
	}
	return completed, nil
}

func compareEpisode(a, b Item) int {
	return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
}

func episodeSequenceContext(item Item) string {
	directory := filepath.ToSlash(filepath.Dir(item.path))
	parts := strings.Split(directory, "/")
	kept := parts[:0]
	for _, part := range parts {
		if seasonDirectoryNumber(part) == item.Season {
			continue
		}
		kept = append(kept, strings.ToLower(strings.TrimSpace(part)))
	}
	return strings.ToLower(item.rootKind + "\x00" + strings.Join(kept, "/"))
}

func seasonDirectoryNumber(value string) int {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"season", "s"} {
		if !strings.HasPrefix(normalized, prefix) {
			continue
		}
		number := strings.TrimLeft(strings.TrimSpace(strings.TrimPrefix(normalized, prefix)), "._-")
		parsed, err := strconv.Atoi(number)
		if err == nil {
			return parsed
		}
	}
	return -1
}
