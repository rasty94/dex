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

// fakePasswords is a dex API holding local users. Embedding the interface
// leaves every other method nil, so a test that reaches one panics.
type fakePasswords struct {
	api.DexClient
	passwords []*api.Password
	got       *api.UpdatePasswordReq
}

func (f *fakePasswords) ListPasswords(context.Context, *api.ListPasswordReq, ...grpc.CallOption) (*api.ListPasswordResp, error) {
	return &api.ListPasswordResp{Passwords: f.passwords}, nil
}

func (f *fakePasswords) UpdatePassword(_ context.Context, in *api.UpdatePasswordReq, _ ...grpc.CallOption) (*api.UpdatePasswordResp, error) {
	f.got = in
	return &api.UpdatePasswordResp{}, nil
}

func postUserSave(t *testing.T, fake *fakePasswords, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	d, err := newDashboard(&dexClient{api: fake}, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/users/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), sessionCtxKey,
		&session{Email: "admin@example.com", CanWrite: true}))
	rr := httptest.NewRecorder()
	d.handleUserSave(rr, req)
	return rr
}

// dex reads an empty new_groups as "leave the groups alone", because an empty
// repeated field does not travel. Groups grant access, so an admin who removes
// a user's last group must be told it did not happen, not shown a success.
func TestEmptyingAUsersGroupsIsRefused(t *testing.T) {
	fake := &fakePasswords{}
	postUserSave(t, fake, url.Values{
		"editing":    {"1"},
		"email":      {"jane@example.com"},
		"username":   {"jane"},
		"groups":     {"\n  \n"},
		"had_groups": {"1"},
	})
	if fake.got != nil {
		t.Fatalf("an update reached dex (%v), so the page would report success while the groups stay", fake.got)
	}
}

// Editing a user's groups must not force a password change, and must not send
// an empty hash that dex would store as the new password.
func TestEditingGroupsKeepsThePassword(t *testing.T) {
	fake := &fakePasswords{}
	rr := postUserSave(t, fake, url.Values{
		"editing":    {"1"},
		"email":      {"jane@example.com"},
		"username":   {"jane"},
		"groups":     {"ops\n admins \n"},
		"had_groups": {"1"},
	})
	got := fake.got
	if got == nil {
		t.Fatalf("no update reached dex; status %d", rr.Code)
	}
	if got.NewHash != nil {
		t.Errorf("new hash = %q, want none: an empty password box keeps the current one", got.NewHash)
	}
	if len(got.NewGroups) != 2 || got.NewGroups[0] != "ops" || got.NewGroups[1] != "admins" {
		t.Errorf("new groups = %q, want [ops admins]", got.NewGroups)
	}
}

// The edit form is filled from dex, not from the link: the groups would not fit
// in it, and a stale link would write stale values back.
func TestUserFormShowsTheCurrentGroups(t *testing.T) {
	fake := &fakePasswords{passwords: []*api.Password{
		{Email: "jane@example.com", Username: "jane", UserId: "jane", Groups: []string{"admins", "ops"}},
	}}
	d, err := newDashboard(&dexClient{api: fake}, nil, nil, testLogger())
	if err != nil {
		t.Fatalf("newDashboard: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/users/edit?email=jane@example.com", nil)
	req = req.WithContext(context.WithValue(req.Context(), sessionCtxKey,
		&session{Email: "admin@example.com", CanWrite: true, CSRFToken: "csrf-1"}))
	rr := httptest.NewRecorder()
	d.handleUserForm(rr, req)

	body := rr.Body.String()
	for _, want := range []string{
		"admins&#10;ops",                             // the groups, one per line
		`name="had_groups" value="1"`,                // so emptying them can be refused
		`id="username" name="username" value="jane"`, // read from dex, not the URL
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the edit form is missing %q", want)
		}
	}
}
