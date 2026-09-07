package catalog

import "testing"

func TestMatchingAudioSidecarsRequiresSameStemAndLanguage(t *testing.T) {
	got := matchingAudioSidecars("Film 2026.mkv", []sidecarFile{{rel: "Film 2026.fra.Director Commentary.m4a"}, {rel: "Other.fra.m4a"}})
	if len(got) != 1 || got[0].rel != "Film 2026.fra.Director Commentary.m4a" {
		t.Fatalf("matches = %#v", got)
	}
}
