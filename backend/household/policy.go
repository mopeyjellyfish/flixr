package household

import (
	"encoding/json"
	"fmt"

	"github.com/mopeyjellyfish/flixr/backend/access"
)

func (m *Manager) Policy(profileID string) (access.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[profileID]
	if !ok {
		return access.Policy{}, ErrProfileNotFound
	}
	return p.policy, nil
}

// UpdatePolicy commits the new policy and session revocations together. The
// returned values are non-secret session identities for releasing runtime media.
func (m *Manager) UpdatePolicy(profileID string, requested access.Policy) (access.Policy, []string, error) {
	policy, err := requested.Validate()
	if err != nil {
		return access.Policy{}, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[profileID]
	if !ok {
		return access.Policy{}, nil, ErrProfileNotFound
	}
	policy.Version = p.policy.Version + 1
	if policy.Version < 1 {
		policy.Version = 1
	}
	var revoked []string
	if m.db != nil {
		libraries, _ := json.Marshal(policy.LibraryIDs)
		allowTags, _ := json.Marshal(policy.AllowTags)
		denyTags, _ := json.Marshal(policy.DenyTags)
		tx, err := m.db.Begin()
		if err != nil {
			return access.Policy{}, nil, fmt.Errorf("begin profile access update: %w", err)
		}
		defer tx.Rollback()
		result, err := tx.Exec(`UPDATE profile_access_policies SET library_ids_json=?,rating_region=?,max_rating=?,unrated_policy=?,allow_tags_json=?,deny_tags_json=?,version=? WHERE profile_id=?`, string(libraries), policy.RatingRegion, policy.MaxRating, policy.Unrated, string(allowTags), string(denyTags), policy.Version, profileID)
		if err != nil {
			return access.Policy{}, nil, fmt.Errorf("save profile access policy: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return access.Policy{}, nil, ErrProfileNotFound
		}
		revoked, err = revokeSubjectSessions(tx, profileID)
		if err != nil {
			return access.Policy{}, nil, fmt.Errorf("revoke profile access sessions: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return access.Policy{}, nil, fmt.Errorf("commit profile access policy: %w", err)
		}
	} else {
		revoked = m.revokeMemorySessionsLocked(profileID)
	}
	p.policy = policy
	m.profiles[profileID] = p
	return policy, revoked, nil
}

func scanPolicy(libraries, region, rating, unrated, allowTags, denyTags string, version int64) (access.Policy, error) {
	p := access.Policy{RatingRegion: region, MaxRating: rating, Unrated: unrated, Version: version}
	if err := json.Unmarshal([]byte(libraries), &p.LibraryIDs); err != nil {
		return access.Policy{}, fmt.Errorf("decode profile library access: %w", err)
	}
	if err := json.Unmarshal([]byte(allowTags), &p.AllowTags); err != nil {
		return access.Policy{}, fmt.Errorf("decode profile allow tags: %w", err)
	}
	if err := json.Unmarshal([]byte(denyTags), &p.DenyTags); err != nil {
		return access.Policy{}, fmt.Errorf("decode profile deny tags: %w", err)
	}
	return p, nil
}
