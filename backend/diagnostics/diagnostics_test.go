package diagnostics

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestArchiveRedactsAndBoundsRecords(t *testing.T) {
	log := New(1)
	log.Record("failure", "token=top-secret path=/Users/owner/Movies/Private Film.mkv password=hunter2")
	log.Record("failure", "another failure")
	if len(log.Records()) != 1 {
		t.Fatal("records were not bounded")
	}
	archive, err := Build(Health{Version: "0.1.0", Runtime: "go-test"}, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	if len(archive) > MaxArchiveBytes {
		t.Fatalf("archive has %d bytes, limit is %d", len(archive), MaxArchiveBytes)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) != 1 {
		t.Fatalf("archive = %v, files=%d", err, len(reader.File))
	}
	f, err := reader.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"top-secret", "hunter2", "/Users/owner", "Private Film"} {
		if strings.Contains(text, secret) {
			t.Fatalf("archive leaked %q: %s", secret, text)
		}
	}
}
