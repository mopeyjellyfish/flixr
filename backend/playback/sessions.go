package playback

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

const directSessionTTL = 15 * time.Minute

// Session is short-lived, profile-bound authority for one catalog item.
type Session struct {
	ID             string    `json:"id"`
	ProfileID      string    `json:"-"`
	CatalogID      string    `json:"catalog_id"`
	Plan           Plan      `json:"plan"`
	PositionMS     int64     `json:"position_ms"`
	StreamOffsetMS int64     `json:"-"`
	GenerationID   string    `json:"generation_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
