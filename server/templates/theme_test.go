package templates

import (
	"net/http/httptest"
	"strings"
	"testing"

	dexweb "github.com/dexidp/dex/web"
)

// PrimaryColor is interpolated into a <style> block, so anything that is not a
// hex color has to be rejected when the config loads. By the time it reaches
// the page it is too late: CSS is not a context html/template escapes for us.
func TestClientThemeValidate(t *testing.T) {
	valid := []string{"", "#fff", "#FFF", "#00aaff", "#00AAFF", "#00aaff80"}
	for _, c := range valid {
		if err := (ClientTheme{PrimaryColor: c}).Validate(); err != nil {
			t.Errorf("PrimaryColor %q should be accepted: %v", c, err)
		}
	}

	invalid := []string{
		"red",                         // named colors are not hex
		"#ff",                         // too short
		"#fffff",                      // five digits
		"#gggggg",                     // not hex digits
		"00aaff",                      // missing the hash
		"#fff;}",                      // closes the rule
		"#fff; } body { display:none", // and opens another
		"#fff\n}",                     // a newline does the same
		"url(javascript:alert(1))",
		"expression(alert(1))",
		"#fff/*", // opens a comment that swallows the rest
	}
	for _, c := range invalid {
		if err := (ClientTheme{PrimaryColor: c}).Validate(); err == nil {
			t.Errorf("PrimaryColor %q should be rejected", c)
		} else if !strings.Contains(err.Error(), "hex color") {
			t.Errorf("PrimaryColor %q: unexpected error %v", c, err)
		}
	}
}

// Per-client branding works in two halves: header.html defines
// --primary-color/--primary-hover for the configured client, and the theme's
// CSS reads them. Only the first half is visible from Go, so without this the
// feature can look fully wired and still paint nothing — which is exactly what
// happened: the variables were set and no selector consumed them.
func TestThemeCSSConsumesTheClientColor(t *testing.T) {
	// The selectors a primaryColor is expected to reach, and the variable each
	// one must read. Checking "some rule uses the variable" is not enough: the
	// button is the whole point, and a theme could satisfy that check while
	// leaving the button a hardcoded hex.
	want := map[string]string{
		".theme-btn--primary":       "var(--primary-color",
		".theme-btn--primary:hover": "var(--primary-hover",
		".theme-form-input:focus":   "var(--primary-color",
	}

	for _, name := range []string{"light", "dark"} {
		t.Run(name, func(t *testing.T) {
			_, themed, _, _, err := LoadWebConfig(Config{
				WebFS: dexweb.FS(), IssuerURL: "http://127.0.0.1:5556", Theme: name,
			})
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest("GET", "/styles.css", nil)
			w := httptest.NewRecorder()
			themed.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("serving the %s theme: status %d", name, w.Code)
			}
			css := w.Body.String()

			for selector, variable := range want {
				block, ok := ruleBlock(css, selector)
				if !ok {
					t.Errorf("%s theme: no %s rule", name, selector)
					continue
				}
				if !strings.Contains(block, variable) {
					t.Errorf("%s theme: %s does not read %s), so a client's primaryColor does not reach it:\n%s",
						name, selector, variable, block)
				}
			}

			// Every use must keep the theme's own color as the fallback, so a
			// deployment that configures nothing renders exactly as before.
			for _, decl := range strings.Split(css, "var(--primary")[1:] {
				head := decl[:min(len(decl), 40)]
				if !strings.Contains(head, ",") {
					t.Errorf("%s theme: a --primary-* use has no fallback: %.40s", name, decl)
				}
			}
		})
	}
}

// ruleBlock returns the declarations of the first rule whose selector list ends
// with selector, so ".theme-btn--primary" does not match ".theme-btn--primary:hover".
func ruleBlock(css, selector string) (string, bool) {
	for i := 0; i < len(css); {
		open := strings.IndexByte(css[i:], '{')
		if open < 0 {
			return "", false
		}
		open += i
		close := strings.IndexByte(css[open:], '}')
		if close < 0 {
			return "", false
		}
		close += open

		selectors := strings.TrimSpace(css[i:open])
		for _, sel := range strings.Split(selectors, ",") {
			if strings.TrimSpace(sel) == selector {
				return css[open+1 : close], true
			}
		}
		i = close + 1
	}
	return "", false
}
