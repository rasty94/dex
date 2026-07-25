package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dexidp/dex/storage"
)

func TestClientThemeValidate(t *testing.T) {
	valid := []string{"", "#abc", "#ff6600", "#FF6600AA"}
	for _, color := range valid {
		require.NoError(t, ClientTheme{PrimaryColor: color}.validate(), color)
	}

	// Anything else would end up inside a <style> block.
	invalid := []string{"red", "#ff66", "#gggggg", "#fff;}body{display:none", "url(x)"}
	for _, color := range invalid {
		require.Error(t, ClientTheme{PrimaryColor: color}.validate(), color)
	}
}

func TestBrand(t *testing.T) {
	httpServer, s := newTestServer(t, func(c *Config) {
		c.Web.ClientThemes = map[string]ClientTheme{
			"themed":     {LogoURL: "https://example.com/themed.png", PrimaryColor: "#ff6600"},
			"color-only": {PrimaryColor: "#123456"},
		}
	})
	defer httpServer.Close()

	require.NoError(t, s.storage.CreateClient(t.Context(), storage.Client{
		ID:      "color-only",
		LogoURL: "https://example.com/from-storage.png",
	}))

	r := httptest.NewRequest(http.MethodGet, "/auth", nil)

	tests := []struct {
		clientID  string
		wantLogo  string
		wantColor string
	}{
		{"themed", "https://example.com/themed.png", "#ff6600"},
		// No logo in the theme, so the client's own LogoURL is used.
		{"color-only", "https://example.com/from-storage.png", "#123456"},
		// Unknown client and no client at all fall back to the global branding.
		{"missing", "", ""},
		{"", "", ""},
	}
	for _, tc := range tests {
		b := s.brand(r, tc.clientID)
		require.Equal(t, tc.wantLogo, b.LogoURL, tc.clientID)
		require.Equal(t, tc.wantColor, b.PrimaryColor, tc.clientID)
		require.Equal(t, "/auth", b.ReqPath)
		require.NotEmpty(t, b.Tr)
	}
}

// The templates read the branding through fields promoted from the embedded
// Brand, so render one to make sure it actually reaches the page.
func TestBrandRendered(t *testing.T) {
	httpServer, s := newTestServer(t, func(c *Config) {
		c.Web.ClientThemes = map[string]ClientTheme{
			"themed": {LogoURL: "https://example.com/themed.png", PrimaryColor: "#ff6600"},
		}
	})
	defer httpServer.Close()

	r := httptest.NewRequest(http.MethodGet, "/auth", nil)

	w := httptest.NewRecorder()
	require.NoError(t, s.templates.login(s.brand(r, "themed"), w, nil))
	require.Contains(t, w.Body.String(), "https://example.com/themed.png")
	require.Contains(t, w.Body.String(), "--primary-color: #ff6600")

	w = httptest.NewRecorder()
	require.NoError(t, s.templates.login(s.brand(r, ""), w, nil))
	require.NotContains(t, w.Body.String(), "--primary-color:")
	require.Contains(t, w.Body.String(), "theme/logo.png")
}

func TestMFATrustCookie(t *testing.T) {
	httpServer, s := newTestServer(t, func(c *Config) {
		c.MFATrust.Enabled = true
	})
	defer httpServer.Close()

	// Connector IDs reach the cookie name, so they must be sanitized.
	require.Equal(t, "dex_mfa_trust_key_stone_", mfaTrustCookieName("key stone;"))

	w := httptest.NewRecorder()
	s.setMFATrustCookie(w, "keystone", "gAAAAA-token")

	cookie := w.Result().Cookies()[0]
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	require.Positive(t, cookie.MaxAge)

	r := httptest.NewRequest(http.MethodGet, "/auth/keystone/login", nil)
	r.AddCookie(cookie)
	require.Equal(t, "gAAAAA-token", s.mfaTrustToken(r, "keystone"))
	require.Empty(t, s.mfaTrustToken(r, "other"))

	w = httptest.NewRecorder()
	s.clearMFATrustCookie(w, "keystone")
	require.Equal(t, -1, w.Result().Cookies()[0].MaxAge)
}
