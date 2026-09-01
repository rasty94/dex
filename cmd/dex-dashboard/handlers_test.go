package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/dexidp/dex/api/v2"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Each page defines a block named "content". Parsed into one template set those
// definitions overwrite each other and every page renders the same body, so
// this asserts the pages are actually distinct. Passing real API types also
// catches a template referencing a field that does not exist.
func TestPagesRenderTheirOwnContent(t *testing.T) {
	d, err := newDashboard(nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	cases := []struct {
		name     string
		data     any
		contains []string
	}{
		{
			name: "clients.html",
			data: []*api.ClientInfo{{
				Id: "example-app", Name: "Example", Public: true,
				RedirectUris: []string{"http://127.0.0.1:5555/callback"},
			}},
			contains: []string{"Redirect URIs", "example-app", "Example", "public", "127.0.0.1:5555/callback"},
		},
		{
			name:     "connectors.html",
			data:     []*api.Connector{{Id: "keystone", Type: "keystone", Name: "OpenStack", Config: []byte(`{"host":"x"}`)}},
			contains: []string{"Config size", "keystone", "OpenStack", "12 bytes"},
		},
		{
			name:     "users.html",
			data:     []*api.Password{{Email: "jane@example.com", Username: "jane", UserId: "u-1"}},
			contains: []string{"local password users only", "jane@example.com", "u-1"},
		},
		{
			name: "sessions.html",
			data: sessionsData{
				Subject:    "CgExEgR0ZXN0",
				Connectors: []string{"local", "mock"},
				Searched:   true,
				Tokens:     []*api.RefreshTokenRef{{Id: "tok-1", ClientId: "example-app", CreatedAt: 1700000000}},
			},
			contains: []string{"Look up", "CgExEgR0ZXN0", "tok-1", "2023-11-14"},
		},
	}

	markers := map[string]string{
		"clients.html": "Redirect URIs", "connectors.html": "Config size",
		"users.html": "local password users only", "sessions.html": "Look up",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			d.render(rr, req, tc.name, page{Title: "T", Nav: "n", Data: tc.data})

			body := rr.Body.String()
			for _, want := range tc.contains {
				if !strings.Contains(body, want) {
					t.Errorf("%s is missing %q", tc.name, want)
				}
			}
			for other, marker := range markers {
				if other != tc.name && strings.Contains(body, marker) {
					t.Errorf("%s leaked content from %s (found %q)", tc.name, other, marker)
				}
			}
		})
	}
}

// A failing dex call must render the page with an explanation, not a blank 500.
func TestRenderListShowsTheError(t *testing.T) {
	d, err := newDashboard(nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/clients", nil)
	d.renderList(rr, req, "clients.html", "Clients", "clients", func() (any, error) {
		return nil, errUnavailable{}
	})

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want a rendered page", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "Cannot reach dex") {
		t.Errorf("error page does not explain the failure, got: %s", body)
	}
}

type errUnavailable struct{}

func (errUnavailable) Error() string {
	return "rpc error: code = Unavailable desc = connection refused"
}

func TestFriendlyGRPCError(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"rpc error: code = Unauthenticated desc = invalid authorization token", "refused the API token"},
		{"rpc error: code = Unavailable desc = connection refused", "Cannot reach dex"},
		{"api_connectors_crud feature flag is not enabled", "feature flag"},
		{"rpc error: code = Unknown desc = proto: cannot parse invalid wire-format data", "not a valid subject"},
		{"something else entirely", "something else entirely"},
	}
	for _, tc := range tests {
		if got := friendlyGRPCError(staticErr(tc.in)); !strings.Contains(got, tc.want) {
			t.Errorf("friendlyGRPCError(%q) = %q, want it to mention %q", tc.in, got, tc.want)
		}
	}
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

// The session cookie must not be readable from JavaScript, and must be marked
// Secure whenever the panel is served over HTTPS.
func TestSessionCookieFlags(t *testing.T) {
	for _, tc := range []struct {
		baseURL    string
		wantSecure bool
	}{
		{"https://dash.example.com", true},
		{"http://localhost:5556", false},
	} {
		a := &authenticator{secure: strings.HasPrefix(tc.baseURL, "https://")}
		c := a.cookie(sessionCookie, "value", int(time.Hour/time.Second))
		if !c.HttpOnly {
			t.Errorf("%s: session cookie must be HttpOnly", tc.baseURL)
		}
		if c.Secure != tc.wantSecure {
			t.Errorf("%s: Secure = %v, want %v", tc.baseURL, c.Secure, tc.wantSecure)
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("%s: SameSite = %v, want Lax", tc.baseURL, c.SameSite)
		}
	}
}
