// Package diagnostics creates deliberately small, local support exports.
package diagnostics

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"time"
)

const MaxArchiveBytes = 128 << 10

type Health struct {
	Version           string `json:"version"`
	Revision          string `json:"revision,omitempty"`
	Runtime           string `json:"runtime"`
	Claimed           bool   `json:"claimed"`
	FFprobe           bool   `json:"ffprobe"`
	FFmpeg            bool   `json:"ffmpeg"`
	ScanStatus        string `json:"scan_status"`
	ScanFailed        int    `json:"scan_failed"`
	ActiveGenerations int    `json:"active_generations"`
}

type Record struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	ErrorID string    `json:"error_id,omitempty"`
}

type Log struct {
	mu      sync.Mutex
	limit   int
	records []Record
}

func New(limit int) *Log { return &Log{limit: limit} }

// Record intentionally retains no free-form text: errors often contain paths or credentials.
func (l *Log) Record(event, _ string) {
	l.record(Record{At: time.Now().UTC(), Event: event})
}

func (l *Log) RecordID(event, errorID string) {
	l.record(Record{At: time.Now().UTC(), Event: event, ErrorID: errorID})
}

func (l *Log) record(record Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit <= 0 {
		return
	}
	if len(l.records) == l.limit {
		copy(l.records, l.records[1:])
		l.records = l.records[:l.limit-1]
	}
	l.records = append(l.records, record)
}

func (l *Log) Records() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Record(nil), l.records...)
}

func Build(health Health, records []Record) ([]byte, error) {
	if health.Runtime == "" {
		health.Runtime = runtime.Version()
	}
	payload, err := json.Marshal(struct {
		Health   Health   `json:"health"`
		Failures []Record `json:"failures"`
	}{health, records})
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxArchiveBytes/2 {
		return nil, errors.New("diagnostic payload exceeds limit")
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	file, err := archive.Create("flixr-diagnostics.json")
	if err == nil {
		_, err = file.Write(payload)
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if output.Len() > MaxArchiveBytes {
		return nil, errors.New("diagnostic archive exceeds limit")
	}
	return output.Bytes(), nil
}
