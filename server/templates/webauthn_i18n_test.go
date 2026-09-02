package templates

import (
	"net/http/httptest"
	"strings"
	"testing"

	dexweb "github.com/dexidp/dex/web"
)

// The WebAuthn page is the only one whose strings live inside its <script>, so
// they are emitted in a JavaScript context rather than an HTML one. That is the
// reason it was left out of the first i18n pass: getting it wrong does not
// render a wrong label, it produces broken JavaScript and a page whose button
// does nothing.
func TestWebAuthnScriptStringsAreValidJS(t *testing.T) {
	//nolint:dogsled // only the templates are needed here
	_, _, _, tmpls, err := LoadWebConfig(Config{WebFS: dexweb.FS(), IssuerURL: "http://127.0.0.1:5556"})
	if err != nil {
		t.Fatal(err)
	}

	for _, lang := range []string{"en", "es", "fr", "de", "pt"} {
		t.Run(lang, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/mfa/webauthn", nil)
			r.Header.Set("Accept-Language", lang)
			w := httptest.NewRecorder()
			if err := tmpls.WebAuthnVerify(r, w, "login", "totp"); err != nil {
				t.Fatalf("rendering failed: %v", err)
			}
			body := w.Body.String()

			tr := translations[lang]

			// Each script string must reach the page as the translated text. An
			// apostrophe may arrive raw or as \u0027 depending on how the value
			// is emitted; both read back as the same string in JavaScript, so the
			// test accepts either rather than pinning one form.
			for _, key := range []string{"webauthn_canceled", "webauthn_not_allowed", "webauthn_unexpected"} {
				raw := tr[key]
				escaped := strings.ReplaceAll(raw, "'", `\u0027`)
				if !strings.Contains(body, raw) && !strings.Contains(body, escaped) {
					t.Errorf("%s: %q did not reach the page", key, raw)
				}
			}

			// What must never happen is HTML escaping inside the script: the user
			// would read the escape sequence itself in the error box.
			if strings.Contains(body, "&#39;") {
				t.Errorf("a script string was HTML-escaped instead of JS-escaped")
			}

			// And the visible headings must be translated too.
			if !strings.Contains(body, tr["webauthn_verify_title"]) {
				t.Errorf("the page heading is not in %s", lang)
			}
		})
	}
}
