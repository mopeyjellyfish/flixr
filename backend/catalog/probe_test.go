package catalog

import (
	"context"
	"os"
	"testing"
)

type probeRunner func(context.Context, string, []string, []*os.File) ([]byte, error)

func (f probeRunner) Output(ctx context.Context, name string, args []string, files []*os.File) ([]byte, error) {
	return f(ctx, name, args, files)
}

func TestFFprobeNormalizesMissingAndInvalidProperties(t *testing.T) {
	tests := []struct {
		name     string
		duration string
		wantMS   int64
	}{
		{name: "missing", wantMS: 0},
		{name: "not a number", duration: "NaN", wantMS: 0},
		{name: "infinity", duration: "+Inf", wantMS: 0},
		{name: "overflow", duration: "1e300", wantMS: 0},
		{name: "valid", duration: "2.025", wantMS: 2025},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := `{"format":{"format_name":"matroska","duration":"` + tt.duration + `"},"streams":[{"codec_type":"video","codec_name":"h264","width":-1,"height":-1},{"codec_type":"audio","codec_name":"aac","channels":-1},{"index":-2,"codec_type":"subtitle","codec_name":"subrip"},{"index":0,"codec_type":"audio","codec_name":"aac","channels":2}]}`
			prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(payload), nil })}
			file, err := os.CreateTemp(t.TempDir(), "media")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			got, err := prober.Probe(context.Background(), file)
			if err != nil {
				t.Fatal(err)
			}
			if got.DurationMS != tt.wantMS || got.PrimaryVideoStreamIndex != -1 || got.Width != 0 || got.Height != 0 || got.Audio[0].Index != -1 || got.Audio[0].Channels != 0 || got.Audio[1].Index != 0 || got.Subtitles[0].Index != -1 {
				t.Fatalf("properties = %#v", got)
			}
		})
	}
}

func TestFFprobeRejectsMalformedOutput(t *testing.T) {
	prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(`{`), nil })}
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := prober.Probe(context.Background(), file); err == nil {
		t.Fatal("Probe accepted malformed JSON")
	}
}

func TestFFprobeRecordsFrameRateAndBitDepth(t *testing.T) {
	payload := `{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"2"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","avg_frame_rate":"30000/1001","bits_per_raw_sample":"10"},{"index":1,"codec_type":"audio","codec_name":"aac","channels":6}]}`
	prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(payload), nil })}
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := prober.Probe(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	if got.FrameRateMilli != 29970 || got.BitDepth != 10 || got.Audio[0].Channels != 6 {
		t.Fatalf("properties = %#v", got)
	}
}

func TestFFprobeNormalizesSDRAndUsesGreatestValidBitDepth(t *testing.T) {
	payload := `{"format":{},"streams":[{"codec_type":"video","codec_name":"h264","bits_per_sample":8,"bits_per_raw_sample":"10","color_transfer":"bt709"}]}`
	prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(payload), nil })}
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := prober.Probe(context.Background(), file)
	if err != nil || got.BitDepth != 10 || got.HDR != "" {
		t.Fatalf("properties = %#v, err = %v", got, err)
	}
	if hdrTransfer("smpte2084") != "smpte2084" || hdrTransfer("arib-std-b67") != "arib-std-b67" {
		t.Fatal("HDR transfer normalization")
	}
}
