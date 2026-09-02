package templates

import (
	"strings"
	"testing"
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
