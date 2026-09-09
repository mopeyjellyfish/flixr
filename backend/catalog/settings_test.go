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

type disableDuringScanProvider struct {
	calls   int
	started chan struct{}
	release chan struct{}
}

func (p *disableDuringScanProvider) Lookup(context.Context, string, string, string) (Enrichment, error) {
	p.calls++
	if p.calls == 1 {
		close(p.started)
		<-p.release
	}
	return Enrichment{ProviderID: "7", Title: "Film"}, nil
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
	var recordedBefore string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='metadata_credential_revision'").Scan(&recordedBefore); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if status := c.MetadataStatus(); status.State != "rate_limited" {
		t.Fatalf("rate-limited status = %+v", status)
	}
	var recordedAfter string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='metadata_credential_revision'").Scan(&recordedAfter); err != nil || recordedAfter != recordedBefore {
		t.Fatalf("failed refresh recorded credential revision: before=%q after=%q err=%v", recordedBefore, recordedAfter, err)
	}
}

func TestPartialLocalScanRecordsBoundedCredentialRefresh(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) {
		return MediaProperties{}, os.ErrInvalid
	}))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(root+"/Broken.mp4", []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.SetApplicationTMDBToken("application-one")
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if status := c.ScanStatus(); status.Status != "partial" || status.Failed != 1 {
		t.Fatalf("scan status = %+v", status)
	}
	var revision string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='metadata_credential_revision'").Scan(&revision); err != nil || revision == "" {
		t.Fatalf("partial bounded refresh revision = %q, %v", revision, err)
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

func TestCredentialRefreshRemainsDueUntilEveryLibraryCompletes(t *testing.T) {
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
	emptyRoot := t.TempDir()
	archiveRoot := t.TempDir()
	if err := os.WriteFile(archiveRoot+"/Film.mp4", []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(emptyRoot, ""); err != nil {
		t.Fatal(err)
	}
	archive, err := c.CreateLibrary("Archive", "film")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddLibraryLocation(archive.ID, archiveRoot); err != nil {
		t.Fatal(err)
	}
	provider := &revisionProvider{}
	c.SetProvider(provider)
	c.SetApplicationTMDBToken("application-one")
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	c.SetApplicationTMDBToken("application-two")
	if err := c.scanLibrary(t.Context(), 1, "films", nil); err != nil {
		t.Fatal(err)
	}
	if !c.metadataRefreshDueFor(map[string]bool{archive.ID: true}, false) {
		t.Fatal("empty first library prematurely completed the credential refresh batch")
	}
	afterEmpty := provider.byID
	if err := c.scanLibrary(t.Context(), 1, archive.ID, nil); err != nil {
		t.Fatal(err)
	}
	due := c.metadataRefreshDueFor(nil, false)
	if provider.byID != afterEmpty+1 || due {
		t.Fatalf("archive refresh byID=%d due=%v", provider.byID, due)
	}
}

func TestMetadataRefreshDueIncludesNamedLibraryLocations(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	library, err := c.CreateLibrary("Archive", "film")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddLibraryLocation(library.ID, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	c.SetApplicationTMDBToken("application-one")
	if !c.MetadataRefreshDue() {
		t.Fatal("named-only library location was omitted from metadata refresh readiness")
	}
}

func TestMetadataWithoutApplicationCredentialIsTruthfullyUnavailable(t *testing.T) {
	status := New().MetadataStatus()
	if status.Configured || !status.Enabled || status.Source != "none" || status.State != "unavailable" {
		t.Fatalf("status = %+v", status)
	}
}

func TestMetadataProviderOutageIsTruthfullyUnavailableWithoutDatabase(t *testing.T) {
	c := New()
	c.SetApplicationTMDBToken("application-token")
	c.noteMetadataFailure(ErrProviderUnavailable)
	status := c.MetadataStatus()
	if status.State != "unavailable" || !status.Configured || status.Source != "application" {
		t.Fatalf("outage status = %+v", status)
	}
}

func TestDisablingMetadataDuringScanStopsSubsequentProviderCalls(t *testing.T) {
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
	for _, name := range []string{"First.mp4", "Second.mp4"} {
		if err := os.WriteFile(root+"/"+name, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	provider := &disableDuringScanProvider{started: make(chan struct{}), release: make(chan struct{})}
	c.SetProvider(provider)
	c.SetApplicationTMDBToken("application-token")
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.StartScan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	<-provider.started
	if err := c.SetMetadataEnabled(false); err != nil {
		t.Fatal(err)
	}
	c.mu.RLock()
	done := c.done
	c.mu.RUnlock()
	close(provider.release)
	<-done
	if provider.calls != 1 {
		t.Fatalf("provider calls after disable = %d, want 1 in-flight call only", provider.calls)
	}
}
