package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc"

	api "github.com/dexidp/dex/api/v2"
)

// captureClientUpdates is a dex API that records the update it was handed.
// Embedding the interface leaves every other method nil, which is what we want:
// a test that reaches one would panic rather than pass quietly.
type captureClientUpdates struct {
	api.DexClient
	got *api.UpdateClientReq
}

func (c *captureClientUpdates) UpdateClient(_ context.Context, in *api.UpdateClientReq, _ ...grpc.CallOption) (*api.UpdateClientResp, error) {
	c.got = in
	return &api.UpdateClientResp{}, nil
}

// dex made backchannelLogoutUri and refreshTokenLifetime optional so that an
// empty value clears them: without explicit presence a client could be given a
// back-channel endpoint and never relieved of one. The dashboard has to send
// them as pointers even when the box is empty, or clearing silently does
// nothing and dex keeps posting logout tokens at a dead endpoint.
func TestEmptyingAClientsLogoutFieldsClearsThem(t *testing.T) {
	fake := &captureClientUpdates{}
	d, err := newDashboard(&dexClient{api: fake}, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	form := url.Values{
		"editing":                   {"1"},
		"id":                        {"example-app"},
		"name":                      {"Example"},
		"redirect_uris":             {"https://example.com/cb"},
		"allowed_connectors":        {"local\nkeystone"},
		"sso_shared_with":           {"other-app"},
		"post_logout_redirect_uris": {"https://example.com/bye"},
		"backchannel_logout_uri":    {""},
		"refresh_token_lifetime":    {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/clients/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), sessionCtxKey,
		&session{Email: "admin@example.com", CanWrite: true}))

	d.handleClientSave(httptest.NewRecorder(), req)

	got := fake.got
	if got == nil {
		t.Fatal("no update reached dex")
	}
	if got.BackchannelLogoutUri == nil {
		t.Error("an emptied back-channel URI was not sent, so dex leaves the old one in place")
	} else if *got.BackchannelLogoutUri != "" {
		t.Errorf("back-channel URI = %q, want empty", *got.BackchannelLogoutUri)
	}
	if got.RefreshTokenLifetime == nil {
		t.Error("an emptied refresh token lifetime was not sent, so the client stays on the old one")
	}

	// The lists travel as themselves; the form says they cannot be cleared.
	if len(got.AllowedConnectors) != 2 || got.AllowedConnectors[0] != "local" {
		t.Errorf("allowed connectors = %v", got.AllowedConnectors)
	}
	if len(got.SsoSharedWith) != 1 || got.SsoSharedWith[0] != "other-app" {
		t.Errorf("sso shared with = %v", got.SsoSharedWith)
	}
	if len(got.PostLogoutRedirectUris) != 1 {
		t.Errorf("post-logout redirect URIs = %v", got.PostLogoutRedirectUris)
	}
}

// A field the form does not render cannot be edited, and nothing else fails: the
// page still looks complete. So the values have to be asserted, per field.
func TestClientFormRendersTheSessionFields(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/clients/edit", nil)
	req = req.WithContext(context.WithValue(req.Context(), sessionCtxKey,
		&session{Email: "admin@example.com", CanWrite: true, CSRFToken: "csrf-1"}))
	d.render(rr, req, "client_form.html", page{Title: "Edit", Nav: "clients", Data: struct {
		Client  *api.Client
		Editing bool
	}{
		Client: &api.Client{
			Id: "example-app", Name: "Example",
			AllowedConnectors:      []string{"local", "keystone"},
			SsoSharedWith:          []string{"other-app"},
			BackchannelLogoutUri:   "https://example.com/backchannel",
			PostLogoutRedirectUris: []string{"https://example.com/bye"},
			RefreshTokenLifetime:   "session",
		},
		Editing: true,
	}})
	body := rr.Body.String()

	for _, want := range []string{
		"local&#10;keystone",              // the connector restriction, one per line
		"other-app",                       // who may reuse this client's session
		"https://example.com/backchannel", // where the logout token goes
		"https://example.com/bye",
		`<option value="session" selected>`, // the current lifetime, preselected
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the client form is missing %q", want)
		}
	}
}
