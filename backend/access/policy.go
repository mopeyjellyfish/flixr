// Package access defines the profile content policy shared by catalog and media surfaces.
package access

import (
	"errors"
	"sort"
	"strings"
)

const (
	UnratedAllow = "allow"
	UnratedDeny  = "deny"
)

var ErrInvalidPolicy = errors.New("invalid profile access policy")

// Policy is deny-by-default only for the dimensions that an owner configures.
// Its zero value preserves the historical unrestricted profile behavior.
type Policy struct {
	LibraryIDs   []string `json:"library_ids"`
	RatingRegion string   `json:"rating_region,omitempty"`
	MaxRating    string   `json:"max_rating,omitempty"`
	Unrated      string   `json:"unrated_policy"`
	AllowTags    []string `json:"allow_tags"`
	DenyTags     []string `json:"deny_tags"`
	Version      int64    `json:"version"`
}

type Content struct {
	LibraryIDs []string
	Rating     string
	Tags       []string
}

var ratingLevels = map[string]map[string]int{
	"US": {"G": 1, "TV-Y": 1, "TV-Y7": 1, "TV-G": 1, "PG": 2, "TV-PG": 2, "PG-13": 3, "TV-14": 3, "R": 4, "TV-MA": 4, "NC-17": 5},
	"GB": {"U": 1, "PG": 2, "12": 3, "12A": 3, "15": 4, "18": 5, "R18": 6},
}

// Unrestricted returns the normalized default policy. Its slices are initialized
// so API responses consistently encode empty collections as [] rather than null.
func Unrestricted() Policy {
	policy, _ := (Policy{}).Validate()
	return policy
}

func (p Policy) Restricted() bool {
	return len(p.LibraryIDs) > 0 || p.MaxRating != "" || len(p.AllowTags) > 0 || len(p.DenyTags) > 0
}

func (p Policy) Validate() (Policy, error) {
	p.LibraryIDs = normalized(p.LibraryIDs, false)
	p.AllowTags = normalized(p.AllowTags, true)
	p.DenyTags = normalized(p.DenyTags, true)
	p.RatingRegion = strings.ToUpper(strings.TrimSpace(p.RatingRegion))
	p.MaxRating = strings.ToUpper(strings.TrimSpace(p.MaxRating))
	if p.Unrated == "" {
		p.Unrated = UnratedAllow
	}
	if p.Unrated != UnratedAllow && p.Unrated != UnratedDeny {
		return Policy{}, ErrInvalidPolicy
	}
	if p.MaxRating == "" {
		if p.RatingRegion != "" {
			return Policy{}, ErrInvalidPolicy
		}
	} else {
		levels, ok := ratingLevels[p.RatingRegion]
		if !ok {
			return Policy{}, ErrInvalidPolicy
		}
		if _, ok := levels[p.MaxRating]; !ok {
			return Policy{}, ErrInvalidPolicy
		}
	}
	p.Version = 0
	return p, nil
}

func (p Policy) Allows(content Content) bool {
	if !p.Restricted() {
		return true
	}
	tags := stringSet(normalized(content.Tags, true))
	for _, tag := range p.DenyTags {
		if tags[strings.ToLower(tag)] {
			return false
		}
	}
	if len(p.LibraryIDs) > 0 && !intersects(stringSet(p.LibraryIDs), content.LibraryIDs, false) {
		return false
	}
	if p.MaxRating != "" {
		rating := strings.ToUpper(strings.TrimSpace(content.Rating))
		if rating == "" {
			if p.Unrated != UnratedAllow {
				return false
			}
		} else {
			levels := ratingLevels[p.RatingRegion]
			actual, known := levels[rating]
			limit, validLimit := levels[p.MaxRating]
			if !known || !validLimit || actual > limit {
				return false
			}
		}
	}
	return len(p.AllowTags) == 0 || intersects(stringSet(p.AllowTags), content.Tags, true)
}

func normalized(values []string, fold bool) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if fold {
			value = strings.ToLower(value)
		}
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func intersects(allowed map[string]bool, values []string, fold bool) bool {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if fold {
			value = strings.ToLower(value)
		}
		if allowed[value] {
			return true
		}
	}
	return false
}
