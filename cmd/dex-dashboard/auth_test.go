package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccess(t *testing.T) {
	a := &authenticator{admin: AdminConfig{
		Groups:      []string{"dex-admins", "dex-readers"},
		Emails:      []string{"Break.Glass@example.com"},
		WriteGroups: []string{"dex-admins"},
		WriteEmails: []string{"Break.Glass@example.com"},
	}}

	tests := []struct {
		name      string
		email     string
		groups    []string
		wantRead  bool
		wantWrite bool
	}{
		{"admin group reads and writes", "jane@example.com", []string{"dex-admins"}, true, true},
		{"reader group reads only", "bob@example.com", []string{"dex-readers"}, true, false},
		{"break-glass email reads and writes", "break.glass@example.com", nil, true, true},
		{"authenticated but not an admin", "john@example.com", []string{"eng"}, false, false},
		{"no groups at all", "john@example.com", nil, false, false},
		{"group name is not a substring match", "john@example.com", []string{"dex-admins-readonly"}, false, false},
		{"empty email must not match an empty allow entry", "", nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			read, write := a.access(tc.email, tc.groups)
			if read != tc.wantRead || write != tc.wantWrite {
				t.Errorf("access(%q, %v) = read:%v write:%v, want read:%v write:%v",
					tc.email, tc.groups, read, write, tc.wantRead, tc.wantWrite)
			}
		})
	}
}

// Write permission must never be implied by read permission: leaving the write
// lists empty makes the whole panel read-only, which is the safe default for a
// console that administers the identity provider.
func TestWriteIsNeverImpliedByRead(t *testing.T) {
	a := &authenticator{admin: AdminConfig{Groups: []string{"dex-admins"}}}

	read, write := a.access("jane@example.com", []string{"dex-admins"})
	if !read {
		t.Fatal("an admin-group member should be able to read")
	}
	if write {
		t.Error("write must not be granted when writeGroups and writeEmails are empty")
	}
}

// A user in a write group but in no read group gets nothing: write permission
// is a second gate behind the first, not an alternative way in.
func TestWriteWithoutReadGrantsNothing(t *testing.T) {
	a := &authenticator{admin: AdminConfig{
		Groups:      []string{"dex-admins"},
		WriteGroups: []string{"operators"},
	}}

	read, write := a.access("jane@example.com", []string{"operators"})
	if read || write {
		t.Errorf("a write group alone must not grant access, got read:%v write:%v", read, write)
	}
}

// A config with no admin group and no admin email would admit every user dex
// can authenticate, which for an admin panel is no gate at all.
func TestConfigRefusesAnEmptyAdminGate(t *testing.T) {
	base := func() *Config {
		return &Config{
			BaseURL: "https://dash.example.com",
			Dex:     DexConfig{GRPCAddress: "127.0.0.1:5557"},
			OIDC:    OIDCConfig{Issuer: "https://dex.example.com", ClientID: "dashboard"},
		}
	}

	c := base()
	if err := c.validate(); err == nil {
		t.Fatal("expected an error when neither admin.groups nor admin.emails is set")
	}

	c = base()
	c.Admin.Groups = []string{"dex-admins"}
	if err := c.validate(); err != nil {
		t.Fatalf("unexpected error with an admin group set: %v", err)
	}

	c = base()
	c.Admin.Emails = []string{"jane@example.com"}
	if err := c.validate(); err != nil {
		t.Fatalf("unexpected error with an admin email set: %v", err)
	}
}

func TestRequireAdmin(t *testing.T) {
	a := &authenticator{sessions: newSessionStore(time.Hour)}
	reached := false
	h := a.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if sessionFrom(r.Context()) == nil {
			t.Error("handler ran without a session in context")
		}
		w.WriteHeader(http.StatusOK)
	})

	// No cookie: a GET starts a login, and the protected handler must not run.
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/clients", nil))
	if reached {
		t.Error("protected handler ran without a session")
	}

	// No cookie on a POST is refused outright rather than redirected into a
	// login, so a stale form cannot be replayed.
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/logout", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("POST without a session: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	if reached {
		t.Error("protected handler ran on an unauthenticated POST")
	}

	// A forged session id must not be accepted.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/clients", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "not-a-real-session"})
	h(rr, req)
	if reached {
		t.Error("protected handler ran with an unknown session id")
	}

	// A real session gets through.
	id, err := a.sessions.create(&session{Email: "jane@example.com"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/clients", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
	h(rr, req)
	if !reached {
		t.Error("protected handler did not run with a valid session")
	}
}

func TestSessionExpires(t *testing.T) {
	st := newSessionStore(time.Hour)
	id, err := st.create(&session{Email: "jane@example.com"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, ok := st.get(id); !ok {
		t.Fatal("fresh session should be readable")
	}

	// Expiry is enforced on read, not only by the cookie's max-age, so a client
	// that keeps presenting the id past the TTL still loses access.
	st.s[id].Expiry = time.Now().Add(-time.Second)
	if _, ok := st.get(id); ok {
		t.Error("expired session should not be readable")
	}
}

func TestRequireCSRF(t *testing.T) {
	a := &authenticator{sessions: newSessionStore(time.Hour), logger: testLogger()}
	sess := &session{Email: "jane@example.com", CSRFToken: "the-real-token"}
	id, err := a.sessions.create(sess)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	reached := false
	h := a.requireAdmin(a.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	post := func(token string) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest(http.MethodPost, "/logout?csrf_token="+token, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
		rr := httptest.NewRecorder()
		h(rr, req)
		return rr
	}

	if rr := post("wrong-token"); rr.Code != http.StatusForbidden || reached {
		t.Errorf("bad CSRF token: got %d (handler reached: %v), want %d", rr.Code, reached, http.StatusForbidden)
	}
	if rr := post(""); rr.Code != http.StatusForbidden || reached {
		t.Errorf("missing CSRF token: got %d (handler reached: %v), want %d", rr.Code, reached, http.StatusForbidden)
	}
	if post("the-real-token"); !reached {
		t.Error("valid CSRF token should reach the handler")
	}
}

// A read-only session must not be able to reach a write handler even by posting
// the URL directly: hiding the button is presentation, requireWrite is the gate.
func TestRequireWrite(t *testing.T) {
	a := &authenticator{sessions: newSessionStore(time.Hour), logger: testLogger()}

	reached := false
	h := a.requireAdmin(a.requireWrite(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	post := func(canWrite bool) *httptest.ResponseRecorder {
		reached = false
		id, err := a.sessions.create(&session{Email: "jane@example.com", CanWrite: canWrite})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/clients/delete", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
		rr := httptest.NewRecorder()
		h(rr, req)
		return rr
	}

	if rr := post(false); rr.Code != http.StatusForbidden || reached {
		t.Errorf("read-only session: got %d (handler reached: %v), want %d", rr.Code, reached, http.StatusForbidden)
	}
	if post(true); !reached {
		t.Error("a session with write permission should reach the handler")
	}
}

// The write routes must chain the CSRF check too, so a cross-site POST from a
// logged-in administrator's browser cannot delete anything.
func TestWriteRoutesRequireCSRF(t *testing.T) {
	a := &authenticator{sessions: newSessionStore(time.Hour), logger: testLogger()}
	id, err := a.sessions.create(&session{Email: "jane@example.com", CanWrite: true, CSRFToken: "real"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	reached := false
	h := a.requireAdmin(a.requireWrite(a.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})))

	req := httptest.NewRequest(http.MethodPost, "/sessions/revoke?csrf_token=forged", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusForbidden || reached {
		t.Errorf("forged CSRF token: got %d (handler reached: %v), want %d", rr.Code, reached, http.StatusForbidden)
	}
}
