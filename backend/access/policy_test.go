package access_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyDecisionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		policy  access.Policy
		content access.Content
		allowed bool
	}{
		{name: "unrestricted default", allowed: true},
		{name: "allowed library", policy: access.Policy{LibraryIDs: []string{"kids"}}, content: access.Content{LibraryIDs: []string{"kids"}}, allowed: true},
		{name: "new library excluded", policy: access.Policy{LibraryIDs: []string{"kids"}}, content: access.Content{LibraryIDs: []string{"new"}}},
		{name: "shared title has allowed source", policy: access.Policy{LibraryIDs: []string{"kids"}}, content: access.Content{LibraryIDs: []string{"adult", "kids"}}, allowed: true},
		{name: "allow tag required", policy: access.Policy{AllowTags: []string{"family"}}, content: access.Content{Tags: []string{"family"}}, allowed: true},
		{name: "missing allow tag", policy: access.Policy{AllowTags: []string{"family"}}, content: access.Content{Tags: []string{"drama"}}},
		{name: "deny wins over allow", policy: access.Policy{AllowTags: []string{"family"}, DenyTags: []string{"scary"}}, content: access.Content{Tags: []string{"family", "scary"}}},
		{name: "tag allow cannot bypass library", policy: access.Policy{LibraryIDs: []string{"kids"}, AllowTags: []string{"family"}}, content: access.Content{LibraryIDs: []string{"adult"}, Tags: []string{"family"}}},
		{name: "US rating below limit", policy: access.Policy{RatingRegion: "US", MaxRating: "PG-13", Unrated: access.UnratedDeny}, content: access.Content{Rating: "PG"}, allowed: true},
		{name: "US rating above limit", policy: access.Policy{RatingRegion: "US", MaxRating: "PG-13", Unrated: access.UnratedDeny}, content: access.Content{Rating: "R"}},
		{name: "GB rating aliases", policy: access.Policy{RatingRegion: "GB", MaxRating: "12", Unrated: access.UnratedDeny}, content: access.Content{Rating: "12A"}, allowed: true},
		{name: "unrated allowed explicitly", policy: access.Policy{RatingRegion: "GB", MaxRating: "12", Unrated: access.UnratedAllow}, content: access.Content{}, allowed: true},
		{name: "unrated denied explicitly", policy: access.Policy{RatingRegion: "GB", MaxRating: "12", Unrated: access.UnratedDeny}, content: access.Content{}},
		{name: "unknown rating fails closed", policy: access.Policy{RatingRegion: "GB", MaxRating: "12", Unrated: access.UnratedAllow}, content: access.Content{Rating: "TV-MA"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.allowed, tt.policy.Allows(tt.content)) })
	}
}

func TestPolicyValidationNormalizesValues(t *testing.T) {
	policy, err := (access.Policy{LibraryIDs: []string{" kids ", "kids"}, RatingRegion: "gb", MaxRating: "12a", Unrated: access.UnratedDeny, AllowTags: []string{" Family ", "family"}, DenyTags: []string{"Scary"}}).Validate()
	require.NoError(t, err)
	assert.Equal(t, access.Policy{LibraryIDs: []string{"kids"}, RatingRegion: "GB", MaxRating: "12A", Unrated: access.UnratedDeny, AllowTags: []string{"family"}, DenyTags: []string{"scary"}}, policy)

	for _, invalid := range []access.Policy{
		{RatingRegion: "CA", MaxRating: "PG"},
		{RatingRegion: "US", MaxRating: "12"},
		{MaxRating: "PG"},
		{Unrated: "sometimes"},
	} {
		_, err := invalid.Validate()
		require.Error(t, err)
	}
}
