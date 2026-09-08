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
	payload := `{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"2","bit_rate":"900000"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","profile":"High","level":40,"avg_frame_rate":"30000/1001","bits_per_raw_sample":"10"},{"index":1,"codec_type":"audio","codec_name":"aac","profile":"LC","channels":6,"sample_rate":"48000","bit_rate":"256000"}]}`
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
	if got.VideoLevel != 40 || got.Bitrate != 900000 || got.FrameRateMilli != 29970 || got.BitDepth != 10 || got.Audio[0].Profile != "LC" || got.Audio[0].Channels != 6 || got.Audio[0].SampleRate != 48000 || got.Audio[0].Bitrate != 256000 {
		t.Fatalf("properties = %#v", got)
	}
}

func TestFFprobeRecordsForcedAndHearingImpairedSubtitleFlags(t *testing.T) {
	payload := `{"format":{},"streams":[{"index":2,"codec_type":"subtitle","codec_name":"subrip","disposition":{"forced":1,"hearing_impaired":1}}]}`
	prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(payload), nil })}
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := prober.Probe(context.Background(), file)
	if err != nil || len(got.Subtitles) != 1 || !got.Subtitles[0].Forced || !got.Subtitles[0].SDH {
		t.Fatalf("subtitle properties = %#v, err = %v", got.Subtitles, err)
	}
}

func TestFFprobeUsesTheConservativeVariableFrameRateCeiling(t *testing.T) {
	payload := `{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":320,"height":180,"r_frame_rate":"60/1","avg_frame_rate":"180/7"}]}`
	prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(payload), nil })}
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := prober.Probe(context.Background(), file)
	if err != nil || got.FrameRateMilli != 60000 {
		t.Fatalf("properties = %#v, err = %v", got, err)
	}
}

func TestFFprobeReportsRotatedDisplayDimensions(t *testing.T) {
	for _, tt := range []struct {
		name          string
		rotation      string
		width, height int
	}{{"quarter turn", "90", 1080, 1920}, {"negative quarter turn", "-90", 1080, 1920}, {"half turn", "180", 1920, 1080}, {"non-right-angle is unknown", "45", 0, 0}} {
		t.Run(tt.name, func(t *testing.T) {
			payload := `{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"r_frame_rate":"24/1","avg_frame_rate":"24/1","side_data_list":[{"rotation":` + tt.rotation + `}]}]}`
			prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(payload), nil })}
			file, err := os.CreateTemp(t.TempDir(), "media")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			got, err := prober.Probe(context.Background(), file)
			if err != nil || got.Width != tt.width || got.Height != tt.height {
				t.Fatalf("properties = %#v, err = %v", got, err)
			}
		})
	}
}

func TestFFprobeUsesFormatBitrateAsConservativeStreamBound(t *testing.T) {
	payload := `{"format":{"format_name":"matroska,webm","duration":"2","bit_rate":"157945"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","profile":"High","level":12,"width":320,"height":180,"avg_frame_rate":"24/1","bits_per_raw_sample":"8"},{"index":1,"codec_type":"audio","codec_name":"aac","profile":"LC","channels":1,"sample_rate":"48000"}]}`
	prober := ffprobe{runner: probeRunner(func(context.Context, string, []string, []*os.File) ([]byte, error) { return []byte(payload), nil })}
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := prober.Probe(context.Background(), file)
	if err != nil || got.Bitrate != 157945 || got.Audio[0].Bitrate != 157945 {
		t.Fatalf("properties = %#v, err = %v", got, err)
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
