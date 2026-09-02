package templates

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
)

// hexColor matches #rgb, #rrggbb and #rrggbbaa. The value is interpolated into
// a <style> block, so anything else must be rejected at config load rather than
// reaching the page.
var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// ClientTheme overrides the login page branding for a single client_id.
type ClientTheme struct {
	// LogoURL is shown on the login pages. When empty, the LogoURL of the client
	// in storage is used, and failing that the global frontend logo.
	LogoURL string `json:"logoURL"`

	// PrimaryColor is a hex color (#rgb, #rrggbb or #rrggbbaa) for buttons and
	// links.
	PrimaryColor string `json:"primaryColor"`
}

// Validate rejects a color that would not be safe to interpolate into CSS.
func (t ClientTheme) Validate() error {
	if t.PrimaryColor != "" && !hexColor.MatchString(t.PrimaryColor) {
		return fmt.Errorf("primaryColor %q is not a hex color", t.PrimaryColor)
	}
	return nil
}

type clientIDKey struct{}

// WithClientID marks a request as being made on behalf of a client, so the
// pages rendered for it can carry that client's branding. The client id travels
// on the context rather than as an argument because only three of the twelve
// pages are branded, and threading a parameter through the rest to pass "" is
// noise.
func WithClientID(r *http.Request, clientID string) *http.Request {
	if clientID == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), clientIDKey{}, clientID))
}

// clientTheme resolves the branding for the request's client. An absent client,
// an unknown one, or a storage failure all fall back to the global branding,
// which is what the templates render when these fields are empty.
func (t *Templates) clientTheme(r *http.Request) ClientTheme {
	clientID, _ := r.Context().Value(clientIDKey{}).(string)
	if clientID == "" {
		return ClientTheme{}
	}

	theme := t.clientThemes[clientID]
	if theme.LogoURL == "" && t.clientLogo != nil {
		// The client's own logo, which is already part of the storage model, so
		// a deployment gets per-client logos without configuring anything here.
		theme.LogoURL = t.clientLogo(r.Context(), clientID)
	}
	return theme
}
