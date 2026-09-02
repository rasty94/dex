package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ghodss/yaml"

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
	d, err := newDashboard(nil, nil, nil, testLogger())
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
	d, err := newDashboard(nil, nil, nil, testLogger())
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

func TestMatchesFilter(t *testing.T) {
	tests := []struct {
		name   string
		q      string
		fields []string
		want   bool
	}{
		{"an empty filter keeps everything", "", []string{"anything"}, true},
		{"substring match", "app", []string{"example-app"}, true},
		{"case insensitive", "APP", []string{"example-app"}, true},
		{"matches any of the fields", "jane", []string{"example-app", "jane@example.com"}, true},
		{"no match", "zzz", []string{"example-app", "jane@example.com"}, false},
		{"an empty field is not a match", "x", []string{""}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesFilter(tc.q, tc.fields...); got != tc.want {
				t.Errorf("matchesFilter(%q, %v) = %v, want %v", tc.q, tc.fields, got, tc.want)
			}
		})
	}
}

// A filter that hides rows must say so, otherwise the page looks like the data
// is gone rather than merely filtered out.
func TestFilteredListingReportsWhatItHid(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/clients?q=app", nil)
	d.renderFiltered(rr, req, "clients.html", "Clients", "clients", func() (any, int, error) {
		return []*api.ClientInfo{{Id: "example-app"}}, 3, nil
	})

	body := rr.Body.String()
	if !strings.Contains(body, "Showing 1 of 3") {
		t.Errorf("filtered listing does not report what it hid, got: %s", body)
	}
	// The box keeps what was typed, so the filter is visible and clearable.
	if !strings.Contains(body, `value="app"`) {
		t.Error("the filter box lost the query")
	}
}

// "No clients registered" is a lie when there are clients and the filter simply
// hid them. The empty state has to tell the two situations apart.
func TestEmptyStateDistinguishesFilterFromNothing(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	render := func(url string, total int) string {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		d.renderFiltered(rr, req, "clients.html", "Clients", "clients", func() (any, int, error) {
			return []*api.ClientInfo{}, total, nil
		})
		return rr.Body.String()
	}

	filtered := render("/clients?q=zzz", 2)
	if !strings.Contains(filtered, "No clients match") {
		t.Error("a filter with no matches should say so")
	}
	if strings.Contains(filtered, "No clients registered") {
		t.Error("a filter with no matches must not claim there are no clients")
	}
	if !strings.Contains(filtered, "Show all 2") {
		t.Error("the empty state should offer a way back to the full list")
	}

	empty := render("/clients", 0)
	if !strings.Contains(empty, "No clients registered") {
		t.Error("a genuinely empty list should say there are no clients")
	}
}

// The export is the one place credentials leave the system, so what it contains
// has to be exactly what a restore needs and nothing has to be silently lost.
func TestExportBundleShape(t *testing.T) {
	bundle := exportBundle{
		ExportedAt: "2026-09-01T10:00:00Z",
		ExportedBy: "jane@example.com",
		Clients: []exportClient{
			{ID: "app", Name: "App", Secret: "s3cret", RedirectURIs: []string{"https://app/callback"}},
			{ID: "spa", Name: "SPA", Public: true},
		},
		Connectors: []map[string]any{
			{"id": "ldap", "type": "ldap", "name": "LDAP", "config": map[string]any{"bindPW": "el-secreto"}},
		},
	}

	out, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)

	// A backup without secrets cannot restore a confidential client, so they
	// have to be in there. This is deliberate, and why the download is gated.
	for _, want := range []string{"s3cret", "el-secreto", "staticClients", "connectors", "jane@example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("export is missing %q:\n%s", want, got)
		}
	}
	// A public client has no secret, and an empty one must not be written as a
	// key at all: pasting `secret: ""` back into dex creates a broken client.
	if strings.Contains(got, `secret: ""`) {
		t.Errorf("an empty secret was written out:\n%s", got)
	}
}

// The sessions page now shows two different things and the difference matters:
// a browser session is one sign-in, a refresh token is one application's grant.
// Ending the first revokes the second; revoking the second leaves the sign-in
// alone. Conflating them in the UI would make an operator think a user was
// signed out when they were not.
func TestSessionsPageSeparatesBrowsersFromTokens(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	data := sessionsData{
		Subject: "CgExEgR0ZXN0", UserID: "u-1", ConnID: "local",
		Connectors: []string{"local"}, Searched: true,
		Tokens: []*api.RefreshTokenRef{{Id: "tok-1", ClientId: "example-app"}},
		AuthSessions: []*api.AuthSession{{
			Id: "sess-1", ConnectorId: "local", IpAddress: "10.0.0.9",
			UserAgent: "Firefox/1.0", LastActivity: 1700000000, IdleExpiry: 1700003600,
			ClientStates: []*api.ClientAuthState{
				{ClientId: "example-app"},
				{ClientId: "other-app", ViaSso: true},
			},
		}},
	}

	rr := httptest.NewRecorder()
	d.render(rr, httptest.NewRequest(http.MethodGet, "/sessions", nil), "sessions.html",
		page{Title: "Sessions", Nav: "sessions", Data: data})
	body := rr.Body.String()

	for _, want := range []string{
		"Signed-in browsers", "Refresh tokens", // both headings, so neither is mistaken for the other
		"sess-1", "10.0.0.9", "Firefox/1.0",
		"example-app", "other-app",
		"(SSO)", // a client reached through sharing is marked, not shown as a direct login
		"tok-1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the sessions page is missing %q", want)
		}
	}
}

// When dex has the feature switched off, the browser table must say so and the
// refresh tokens must still be listed. Hiding the whole page would lose the half
// that still works.
func TestSessionsPageDegradesWithoutTheFeature(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	d.render(rr, httptest.NewRequest(http.MethodGet, "/sessions", nil), "sessions.html",
		page{Title: "Sessions", Nav: "sessions", Data: sessionsData{
			Subject: "CgExEgR0ZXN0", Searched: true,
			Tokens:              []*api.RefreshTokenRef{{Id: "tok-1", ClientId: "example-app"}},
			SessionsUnavailable: "Browser sessions are gated behind dex's api_sessions_identities_crud feature flag, which is off.",
		}})
	body := rr.Body.String()

	if !strings.Contains(body, "api_sessions_identities_crud") {
		t.Error("the page should explain why the browser table is missing")
	}
	if !strings.Contains(body, "tok-1") {
		t.Error("the refresh tokens must still be listed when sessions are unavailable")
	}
}

// The identity panel exists so an operator can see who they are about to act on
// before acting. The lockout is the field most worth surfacing: a user who
// cannot log in usually looks like a broken connector until you see it.
func TestSessionsPageShowsTheIdentity(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	d.render(rr, httptest.NewRequest(http.MethodGet, "/sessions", nil), "sessions.html",
		page{Title: "Sessions", Nav: "sessions", Data: sessionsData{
			Subject: "CgExEgR0ZXN0", UserID: "u-1", ConnID: "local", Searched: true,
			Identity: &api.UserIdentity{
				UserId: "u-1", ConnectorId: "local",
				Email: "jane@example.com", Username: "jane",
				Groups:       []string{"admins", "devs"},
				BlockedUntil: 1700003600,
				Consents: []*api.ConsentEntry{
					{ClientId: "example-app", Scopes: []string{"openid", "email"}},
				},
				MfaDevices: []*api.MFADeviceInfo{{AuthenticatorId: "totp"}},
			},
		}})
	body := rr.Body.String()

	for _, want := range []string{
		"jane@example.com", "jane", "admins", "devs",
		"Locked out", // the reason a working account looks broken
		"Consents", "example-app", "openid",
		"Second factors", "totp",
		"Withdraw", // the consent action is not offered without write permission, but this page has it
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the identity panel is missing %q", want)
		}
	}
}

// A user dex has never recorded an identity for still has to render: the page
// falls back to the session and token tables rather than failing.
func TestSessionsPageWithoutAnIdentity(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	d.render(rr, httptest.NewRequest(http.MethodGet, "/sessions", nil), "sessions.html",
		page{Title: "Sessions", Nav: "sessions", Data: sessionsData{
			Subject: "CgExEgR0ZXN0", Searched: true,
			Tokens: []*api.RefreshTokenRef{{Id: "tok-1", ClientId: "example-app"}},
		}})
	body := rr.Body.String()

	if strings.Contains(body, "Consents") {
		t.Error("the consents section should be absent when there is no identity")
	}
	if !strings.Contains(body, "tok-1") {
		t.Error("the refresh tokens must still render without an identity")
	}
}

// The erasure is the only action in the dashboard with no undo, so its
// confirmation has to count what goes rather than describe it — and name the
// consequence the action's title does not suggest.
func TestPurgeConfirmationRendersTheInventory(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	d.render(rr, httptest.NewRequest(http.MethodGet, "/sessions/purge", nil), "confirm.html",
		page{Title: "Purge identity", Nav: "sessions", Data: confirmData{
			Action:  "/sessions/purge",
			Fields:  map[string]string{"user_id": "u-1", "conn_id": "keystone"},
			Heading: "Erase jane@example.com on keystone, permanently?",
			Warning: "This is the GDPR erasure.",
			Inventory: []string{
				"2 consent(s) granted to clients",
				"3 signed-in browser(s)",
				"the identity record itself, with its claims and groups",
			},
			Alert:   "This also deletes the local password account jane@example.com, because dex keys passwords by email alone.",
			Confirm: "Erase permanently",
			Cancel:  "/sessions",
		}})
	body := rr.Body.String()

	for _, want := range []string{
		"2 consent(s)", "3 signed-in browser(s)", // counted, not described
		"local password account jane@example.com", // the cross-connector surprise
		`name="user_id" value="u-1"`,              // both identifying fields survive into the POST
		`name="conn_id" value="keystone"`,
		"Erase permanently",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the purge confirmation is missing %q", want)
		}
	}
}

// An action identified by a single field must keep working: the confirmation is
// shared with revoke-all and the connector sign-out.
func TestConfirmationStillCarriesASingleField(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	d.render(rr, httptest.NewRequest(http.MethodGet, "/connectors/sign-out", nil), "confirm.html",
		page{Title: "Sign out", Nav: "connectors", Data: confirmData{
			Action: "/connectors/sign-out", Field: "id", Value: "keystone",
			Heading: "Sign everyone out?", Confirm: "Do it", Cancel: "/connectors",
		}})
	body := rr.Body.String()

	if !strings.Contains(body, `name="id" value="keystone"`) {
		t.Error("the single-field form lost its input")
	}
	if strings.Contains(body, "<ul") {
		t.Error("an action with no inventory should not render an empty list")
	}
}

// The erasure cascades before it reaches the password record, so a failure
// there leaves the user signed out with their identity intact. The message has
// to say which half happened, or the operator retries against a state they do
// not understand.
func TestPurgeFailureExplainsThePartialState(t *testing.T) {
	msg := friendlyGRPCError(errors.New("rpc error: code = Unknown desc = purge password: static passwords: read-only cannot delete password"))

	for _, want := range []string{
		"config file",   // why it failed
		"already ended", // what happened anyway
		"still present", // what did not
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message is missing %q; got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "rpc error") {
		t.Error("the raw gRPC error should not be shown for a case we recognize")
	}
}
