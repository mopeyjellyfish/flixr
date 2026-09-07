package catalog

import "testing"

func TestProviderIdentityRequiresKnownNamespaceKindAndPositiveDecimal(t *testing.T) {
	for _, v := range []struct{ provider, kind, value, want string }{
		{"tmdb", "film", "42", "tmdb:film:42"}, {"tmdb", "series", "0042", "tmdb:series:42"},
		{"tmdb", "film", "", ""}, {"tmdb", "film", "0", ""}, {"tmdb", "film", "-1", ""}, {"tmdb", "film", "4.2", ""}, {"tmdb", "film", "+42", ""}, {"tmdb", "film", " 42", ""}, {"tmdb", "film", "abc", ""}, {"tmdb", "film", "18446744073709551616", ""}, {"other", "film", "42", ""}, {"", "film", "42", ""}, {"tmdb", "episode", "42", ""}, {"tmdb", "unknown", "42", ""},
	} {
		if got := providerIdentity(v.provider, v.kind, v.value); got != v.want {
			t.Errorf("%#v => %q", v, got)
		}
	}
}
