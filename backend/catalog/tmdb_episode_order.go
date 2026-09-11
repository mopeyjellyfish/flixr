package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
)

func (t *TMDB) EpisodeOrderGroups(ctx context.Context, token, seriesID string) ([]EpisodeOrderGroup, error) {
	if t.apiOrigin == nil || !numericID(seriesID) {
		return nil, ErrProviderUnavailable
	}
	u := *t.apiOrigin
	u.Path = path.Join(u.Path, "/3/tv", seriesID, "episode_groups")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, providerHTTPError(resp.StatusCode)
	}
	var body struct {
		Results []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type int    `json:"type"`
		} `json:"results"`
	}
	if err := decodeLimitedJSON(resp.Body, &body); err != nil {
		return nil, err
	}
	out := []EpisodeOrderGroup{}
	for _, g := range body.Results {
		order := ""
		switch g.Type {
		case 1:
			order = "aired"
		case 2:
			order = "absolute"
		case 3:
			order = "dvd"
		}
		if g.ID != "" && order != "" {
			out = append(out, EpisodeOrderGroup{ID: g.ID, Name: g.Name, Order: order})
		}
	}
	return out, nil
}

func (t *TMDB) EpisodeOrderGroup(ctx context.Context, token, groupID string) (ProviderEpisodeOrderGroup, error) {
	if t.apiOrigin == nil || groupID == "" {
		return ProviderEpisodeOrderGroup{}, ErrProviderUnavailable
	}
	u := *t.apiOrigin
	u.Path = path.Join(u.Path, "/3/tv/episode_group", url.PathEscape(groupID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ProviderEpisodeOrderGroup{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := t.do(req)
	if err != nil {
		return ProviderEpisodeOrderGroup{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ProviderEpisodeOrderGroup{}, providerHTTPError(resp.StatusCode)
	}
	var body struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Type   int    `json:"type"`
		Groups []struct {
			Order    int `json:"order"`
			Episodes []struct {
				Order          int `json:"order"`
				SeasonNumber   int `json:"season_number"`
				EpisodeNumber  int `json:"episode_number"`
				AbsoluteNumber int `json:"absolute_number"`
			} `json:"episodes"`
		} `json:"groups"`
	}
	if err := decodeLimitedJSON(resp.Body, &body); err != nil {
		return ProviderEpisodeOrderGroup{}, err
	}
	order := ""
	switch body.Type {
	case 1:
		order = "aired"
	case 2:
		order = "absolute"
	case 3:
		order = "dvd"
	}
	if body.ID == "" || order == "" {
		return ProviderEpisodeOrderGroup{}, fmt.Errorf("invalid episode group")
	}
	out := ProviderEpisodeOrderGroup{EpisodeOrderGroup: EpisodeOrderGroup{ID: body.ID, Name: body.Name, Order: order}}
	sort.SliceStable(body.Groups, func(i, j int) bool { return body.Groups[i].Order < body.Groups[j].Order })
	for i, group := range body.Groups {
		if group.Order < 0 || i > 0 && body.Groups[i-1].Order == group.Order {
			return ProviderEpisodeOrderGroup{}, fmt.Errorf("ambiguous episode group order")
		}
	}
	position := 0
	for _, g := range body.Groups {
		sort.SliceStable(g.Episodes, func(i, j int) bool { return g.Episodes[i].Order < g.Episodes[j].Order })
		for i, episode := range g.Episodes {
			if episode.Order < 0 || i > 0 && g.Episodes[i-1].Order == episode.Order {
				return ProviderEpisodeOrderGroup{}, fmt.Errorf("ambiguous episode order")
			}
		}
		for _, e := range g.Episodes {
			if e.SeasonNumber < 0 || e.EpisodeNumber < 1 {
				continue
			}
			position++
			displaySeason, displayEpisode := e.SeasonNumber, e.EpisodeNumber
			if order == "dvd" {
				displaySeason, displayEpisode = g.Order+1, e.Order+1
			}
			absolute := e.AbsoluteNumber
			if order == "absolute" {
				displaySeason = 1
				if absolute == 0 {
					absolute = position
				}
				displayEpisode = absolute
			}
			out.Episodes = append(out.Episodes, ProviderEpisodeOrderEntry{SourceSeason: e.SeasonNumber, SourceEpisode: e.EpisodeNumber, AbsoluteEpisode: absolute, Mapping: EpisodeOrderPosition{Position: position, EndPosition: position, Season: displaySeason, Episode: displayEpisode, EpisodeEnd: displayEpisode, Special: e.SeasonNumber == 0}})
		}
	}
	return out, nil
}

func numericID(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
