package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestMediaVersionOwnerAndPlaybackContracts(t *testing.T) {
	films := t.TempDir()
	for name, data := range map[string]string{"A 4K.mp4": "four-k", "B 1080.mp4": "full-hd"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(_ context.Context, file *os.File) (catalog.MediaProperties, error) {
		info, _ := file.Stat()
		if strings.Contains(info.Name(), "4K") {
			return catalog.MediaProperties{Container: "mp4", VideoCodec: "hevc", Width: 3840, Height: 2160, Bitrate: 20_000_000, Audio: []catalog.AudioTrack{{Index: 1, Codec: "aac"}}}, nil
		}
		return catalog.MediaProperties{Container: "mp4", VideoCodec: "h264", Width: 1920, Height: 1080, Bitrate: 5_000_000, Audio: []catalog.AudioTrack{{Index: 1, Codec: "aac"}}}, nil
	}))
	if err != nil || c.SetRoots(films, "") != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	canonical, member := items[0], items[1]
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := h.Claim(h.SetupToken(), "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := h.CreateProfile("Viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := h.Select(profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServer(h, c).Handler()
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	groupBody := fmt.Sprintf(`{"kind":"film","canonical_id":%q,"member_ids":[%q]}`, canonical.ID, member.ID)
	if got := request(http.MethodPost, "/api/v1/owner/media-version-groups", groupBody, viewer); got.Code != http.StatusForbidden {
		t.Fatalf("viewer group = %d", got.Code)
	}
	created := request(http.MethodPost, "/api/v1/owner/media-version-groups", groupBody, owner)
	if created.Code != http.StatusCreated || bytes.Contains(created.Body.Bytes(), []byte(films)) || bytes.Contains(created.Body.Bytes(), []byte("relative_path")) {
		t.Fatalf("owner group = %d %s", created.Code, created.Body.String())
	}
	viewerCatalog := request(http.MethodGet, "/api/v1/catalog/view?media=all", "", viewer)
	if viewerCatalog.Code != http.StatusOK || bytes.Contains(viewerCatalog.Body.Bytes(), []byte(fmt.Sprintf(`"id":%q`, member.ID))) {
		t.Fatalf("viewer catalog exposed grouped member = %d %s", viewerCatalog.Code, viewerCatalog.Body.String())
	}
	detail := request(http.MethodGet, "/api/v1/catalog/films/"+canonical.ID, "", viewer)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	var item catalog.Item
	if err := json.Unmarshal(detail.Body.Bytes(), &item); err != nil || len(item.Versions) != 2 {
		t.Fatalf("detail versions = %#v, %v", item.Versions, err)
	}
	h264Capabilities := `{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"],"supports_direct":true,"max_width":1920,"max_height":1080}`
	autoBody := fmt.Sprintf(`{"catalog_id":%q,"version_capabilities":{%q:%s,%q:%s}}`, canonical.ID, canonical.ID, h264Capabilities, member.ID, h264Capabilities)
	auto := request(http.MethodPost, "/api/v1/playback/plans", autoBody, viewer)
	if auto.Code != http.StatusCreated || !bytes.Contains(auto.Body.Bytes(), []byte(fmt.Sprintf(`"id":%q`, member.ID))) || !bytes.Contains(auto.Body.Bytes(), []byte(`"kind":"direct"`)) {
		t.Fatalf("auto plan = %d %s", auto.Code, auto.Body.String())
	}
	var autoPlan struct {
		MediaURL  string `json:"media_url"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(auto.Body.Bytes(), &autoPlan); err != nil {
		t.Fatal(err)
	}
	media := request(http.MethodGet, autoPlan.MediaURL, "", viewer)
	if media.Code != http.StatusOK || media.Body.String() != "full-hd" {
		t.Fatalf("pinned auto media = %d %q", media.Code, media.Body.String())
	}
	qualityBody := fmt.Sprintf(`{"capabilities":%s,"quality":{"mode":"original"},"position_ms":123,"observation":1}`, h264Capabilities)
	quality := request(http.MethodPost, "/api/v1/playback/sessions/"+autoPlan.SessionID+"/quality", qualityBody, viewer)
	if quality.Code != http.StatusOK {
		t.Fatalf("member quality = %d %s", quality.Code, quality.Body.String())
	}
	var canonicalPosition int64
	if err := db.QueryRow(`SELECT position_ms FROM progress WHERE profile_id=? AND catalog_id=?`, profile.ID, canonical.ID).Scan(&canonicalPosition); err != nil || canonicalPosition != 123 {
		t.Fatalf("canonical progress = %d, %v", canonicalPosition, err)
	}
	var memberProgress int
	if err := db.QueryRow(`SELECT COUNT(*) FROM progress WHERE profile_id=? AND catalog_id=?`, profile.ID, member.ID).Scan(&memberProgress); err != nil || memberProgress != 0 {
		t.Fatalf("member progress rows = %d, %v", memberProgress, err)
	}
	incompatibleBody := fmt.Sprintf(`{"catalog_id":%q,"version_id":%q,"version_capabilities":{%q:%s}}`, canonical.ID, canonical.ID, canonical.ID, h264Capabilities)
	incompatible := request(http.MethodPost, "/api/v1/playback/plans", incompatibleBody, viewer)
	if incompatible.Code != http.StatusUnprocessableEntity || !bytes.Contains(incompatible.Body.Bytes(), []byte(`"code":"playback_version_incompatible"`)) || !bytes.Contains(incompatible.Body.Bytes(), []byte(`"alternatives"`)) {
		t.Fatalf("incompatible plan = %d %s", incompatible.Code, incompatible.Body.String())
	}
	var saved int
	if err := db.QueryRow(`SELECT COUNT(*) FROM profile_media_version_preferences`).Scan(&saved); err != nil || saved != 0 {
		t.Fatalf("failed plan persisted preference: %d, %v", saved, err)
	}
	hevcCapabilities := `{"containers":["mp4"],"video_codecs":["hevc"],"audio_codecs":["aac"],"supports_direct":true,"max_width":3840,"max_height":2160}`
	missingMeasurement := request(http.MethodPost, "/api/v1/playback/plans", fmt.Sprintf(`{"catalog_id":%q,"version_id":%q,"version_capabilities":{}}`, canonical.ID, canonical.ID), viewer)
	if missingMeasurement.Code != http.StatusBadRequest {
		t.Fatalf("missing version measurement = %d %s", missingMeasurement.Code, missingMeasurement.Body.String())
	}
	explicitBody := fmt.Sprintf(`{"catalog_id":%q,"version_id":%q,"version_capabilities":{%q:%s}}`, canonical.ID, canonical.ID, canonical.ID, hevcCapabilities)
	explicit := request(http.MethodPost, "/api/v1/playback/plans", explicitBody, viewer)
	if explicit.Code != http.StatusCreated || !bytes.Contains(explicit.Body.Bytes(), []byte(fmt.Sprintf(`"id":%q`, canonical.ID))) {
		t.Fatalf("explicit plan = %d %s", explicit.Code, explicit.Body.String())
	}
	var preference string
	if err := db.QueryRow(`SELECT version_id FROM profile_media_version_preferences WHERE profile_id=?`, profile.ID).Scan(&preference); err != nil || preference != canonical.ID {
		t.Fatalf("saved preference = %q, %v", preference, err)
	}
}
