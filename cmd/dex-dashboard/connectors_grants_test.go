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

type captureConnectorUpdates struct {
	api.DexClient
	stored *api.Connector
	got    *api.UpdateConnectorReq
}

func (c *captureConnectorUpdates) ListConnectors(_ context.Context, _ *api.ListConnectorReq, _ ...grpc.CallOption) (*api.ListConnectorResp, error) {
	return &api.ListConnectorResp{Connectors: []*api.Connector{c.stored}}, nil
}

func (c *captureConnectorUpdates) UpdateConnector(_ context.Context, in *api.UpdateConnectorReq, _ ...grpc.CallOption) (*api.UpdateConnectorResp, error) {
	c.got = in
	return &api.UpdateConnectorResp{}, nil
}

// A connector's grant types can be emptied, unlike a client's lists: the request
// wraps them so "none" is tellable apart from "not mentioned". If the dashboard
// sent a bare nil instead, unchecking every box would silently leave the
// restriction in place — the operator would believe they had lifted it.
func TestUncheckingEveryGrantTypeLiftsTheRestriction(t *testing.T) {
	fake := &captureConnectorUpdates{stored: &api.Connector{
		Id: "mock", Type: "mockCallback", Name: "Mock", Config: []byte("{}"),
		GrantTypes: []string{"authorization_code"},
	}}
	d, err := newDashboard(&dexClient{api: fake}, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	form := url.Values{
		"editing": {"1"}, "id": {"mock"}, "type": {"mockCallback"},
		"name": {"Mock"}, "config": {"{}"},
		// no grant_types at all: every box unchecked
	}
	req := httptest.NewRequest(http.MethodPost, "/connectors/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), sessionCtxKey,
		&session{Email: "admin@example.com", CanWrite: true}))

	d.handleConnectorSave(httptest.NewRecorder(), req)

	if fake.got == nil {
		t.Fatal("no update reached dex")
	}
	if fake.got.NewGrantTypes == nil {
		t.Fatal("grant types were not mentioned, so dex keeps the old restriction")
	}
	if len(fake.got.NewGrantTypes.GrantTypes) != 0 {
		t.Errorf("grant types = %v, want none", fake.got.NewGrantTypes.GrantTypes)
	}
}

// The boxes have to come back checked, or an operator saving an unrelated field
// would quietly drop the restriction they never touched.
func TestConnectorFormChecksTheGrantTypesItHas(t *testing.T) {
	d, err := newDashboard(nil, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connectors/edit", nil)
	req = req.WithContext(context.WithValue(req.Context(), sessionCtxKey,
		&session{Email: "admin@example.com", CanWrite: true, CSRFToken: "csrf-1"}))
	d.render(rr, req, "connector_form.html", page{Title: "Edit", Nav: "connectors", Data: connectorFormData{
		ID: "mock", Type: "mockCallback", Name: "Mock", Config: "{}", Editing: true,
		Types: []string{"mockCallback"}, GrantTypes: []string{"authorization_code"},
		AllGrantTypes: allGrantTypes(),
	}})
	body := rr.Body.String()

	if !strings.Contains(body, `value="authorization_code"
      checked`) && !strings.Contains(body, `value="authorization_code" checked`) {
		t.Error("the grant type the connector has is not checked")
	}
	if strings.Contains(body, `value="implicit" checked`) {
		t.Error("a grant type the connector does not have came back checked")
	}
	if !strings.Contains(body, "implicit") {
		t.Error("the other grant types are not offered at all")
	}
}
