package templates

import (
	"testing"
)

func TestGetTranslationsLanguageSelection(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string // expected login_title
	}{
		{name: "empty header falls back to English", header: "", want: translations["en"]["login_title"]},
		{name: "unknown language falls back to English", header: "kl-KL", want: translations["en"]["login_title"]},
		{name: "exact match", header: "es", want: translations["es"]["login_title"]},
		{name: "region is stripped", header: "es-ES", want: translations["es"]["login_title"]},
		{name: "case is ignored", header: "ES-es", want: translations["es"]["login_title"]},
		{name: "quality values are stripped", header: "es-ES;q=0.9", want: translations["es"]["login_title"]},
		{
			name:   "first supported language in the list wins",
			header: "kl-KL,fr;q=0.9,es;q=0.8",
			want:   translations["fr"]["login_title"],
		},
		{
			name:   "an unsupported first choice does not shadow a supported second",
			header: "kl,de",
			want:   translations["de"]["login_title"],
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetTranslations(tc.header)["login_title"]; got != tc.want {
				t.Errorf("login_title for %q: got %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// Every language must answer for every English key. A missing translation has to
// render the English string, because a blank label is worse than a foreign one —
// an untranslated button still works, an empty button does not.
func TestEveryLanguageCoversEveryEnglishKey(t *testing.T) {
	en := translations["en"]
	if len(en) == 0 {
		t.Fatal("the English translations are empty")
	}

	for lang, tr := range translations {
		for key := range en {
			if tr[key] == "" {
				t.Errorf("%s.yaml: key %q resolves to an empty string", lang, key)
			}
		}
	}
}

// The maps are shared across requests, so a handler must never be able to mutate
// one language's translations into another's.
func TestTranslationsAreSharedNotCopied(t *testing.T) {
	a := GetTranslations("es")
	b := GetTranslations("es")
	if len(a) != len(b) {
		t.Fatalf("two lookups returned different maps: %d vs %d keys", len(a), len(b))
	}
}
