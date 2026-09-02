package templates

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dexweb "github.com/dexidp/dex/web"
)

// The fork's footer shipped a literal "%d" because the template rendered the
// string without interpolating the year. Porting it unchanged would carry the
// bug over, so the year has to actually land.
func TestFooterCopyrightInterpolatesTheYear(t *testing.T) {
	//nolint:dogsled // only the templates are needed here
	_, _, _, tmpls, err := LoadWebConfig(Config{WebFS: dexweb.FS(), IssuerURL: "http://127.0.0.1:5556"})
	if err != nil {
		t.Fatal(err)
	}

	for _, lang := range []string{"en", "es", "fr", "de", "pt"} {
		r := httptest.NewRequest("GET", "/auth", nil)
		r.Header.Set("Accept-Language", lang)
		w := httptest.NewRecorder()
		if err := tmpls.OOB(r, w, "code"); err != nil {
			t.Fatal(err)
		}
		body := w.Body.String()

		want := fmt.Sprintf(translations[lang]["footer_copyright"], time.Now().Year())
		if !strings.Contains(body, want) {
			t.Errorf("%s: expected the footer to read %q", lang, want)
		}
		if strings.Contains(body, "%d") || strings.Contains(body, "%!") {
			t.Errorf("%s: the footer format string reached the page unformatted", lang)
		}
	}
}
