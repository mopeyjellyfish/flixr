package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

type revisionProvider struct {
	byID int
	err  error
}

func (p *revisionProvider) Lookup(context.Context, string, string, string) (Enrichment, error) {
	if p.err != nil {
		return Enrichment{}, p.err
	}
	return Enrichment{ProviderID: "7", Title: "Film", Synopsis: "First"}, nil
}
func (p *revisionProvider) Candidates(context.Context, string, string, string, string, string) ([]Candidate, error) {
	return nil, nil
}
func (p *revisionProvider) ByID(context.Context, string, string, string, string, string) (Enrichment, error) {
	p.byID++
	if p.err != nil {
		return Enrichment{}, p.err
	}
	return Enrichment{ProviderID: "7", Title: "Film", Synopsis: "Renewed"}, nil
}

func TestMetadataCredentialPrecedenceRemovalAndDisable(t *testing.T) {
	c := New()
	c.SetApplicationTMDBToken("application-token")

	status := c.MetadataStatus()
	if !status.Configured || status.Source != "application" || !status.Enabled {
		t.Fatalf("application status = %+v", status)
	}
	if err := c.SetTMDBToken("owner-token"); err != nil {
		t.Fatal(err)
	}
	if status = c.MetadataStatus(); status.Source != "owner" {
		t.Fatalf("owner status = %+v", status)
	}
	if err := c.SetMetadataEnabled(false); err != nil {
		t.Fatal(err)
	}
	if status = c.MetadataStatus(); status.Enabled || status.Configured || status.Source != "disabled" || status.State != "disabled" {
		t.Fatalf("disabled status = %+v", status)
	}
	if err := c.SetMetadataEnabled(true); err != nil {
		t.Fatal(err)
	}
	if status = c.MetadataStatus(); status.Source != "owner" {
		t.Fatalf("re-enabled status = %+v", status)
	}
	if err := c.SetTMDBToken(""); err != nil {
		t.Fatal(err)
	}
	if status = c.MetadataStatus(); !status.Configured || status.Source != "application" {
		t.Fatalf("removed override status = %+v", status)
	}
}

func TestRotatedApplicationCredentialRefreshesAlreadyMatchedMetadata(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		return MediaProperties{VideoCodec: "h264"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(root+"/Film.mp4", []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &revisionProvider{}
	c.SetProvider(provider)
	c.SetApplicationTMDBToken("application-one")
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	c.SetApplicationTMDBToken("application-two")
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if provider.byID != 1 {
		t.Fatalf("credential rotation exact refreshes = %d, want 1", provider.byID)
	}
	c.mu.RLock()
	if len(c.items) != 1 {
		t.Fatalf("refreshed items = %+v", c.items)
	}
	for _, item := range c.items {
		if item.Synopsis != "Renewed" {
			t.Fatalf("refreshed item = %+v", item)
		}
	}
	c.mu.RUnlock()
	provider.err = ErrProviderRateLimited
	c.SetApplicationTMDBToken("application-three")
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if status := c.MetadataStatus(); status.State != "rate_limited" {
		t.Fatalf("rate-limited status = %+v", status)
	}
}

func TestMetadataRefreshDueTracksSuccessfulCredentialRevision(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	c.SetApplicationTMDBToken("application-one")
	if !c.MetadataRefreshDue() {
		t.Fatal("new application credential did not require refresh")
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if c.MetadataRefreshDue() {
		t.Fatal("successful scan did not record credential revision")
	}
	c.SetApplicationTMDBToken("application-two")
	if !c.MetadataRefreshDue() {
		t.Fatal("rotated application credential did not require refresh")
	}
	if err := c.SetMetadataEnabled(false); err != nil {
		t.Fatal(err)
	}
	if c.MetadataRefreshDue() {
		t.Fatal("disabled metadata scheduled a remote refresh")
	}
}

func TestMetadataWithoutApplicationCredentialIsTruthfullyUnavailable(t *testing.T) {
	status := New().MetadataStatus()
	if status.Configured || !status.Enabled || status.Source != "none" || status.State != "unavailable" {
		t.Fatalf("status = %+v", status)
	}
}
