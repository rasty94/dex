package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"golang.org/x/oauth2"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
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
	a := &authenticator{
		sessions: newSessionStore(time.Hour, 0),
		admin:    AdminConfig{Emails: []string{"jane@example.com"}},
		logger:   testLogger(),
	}
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
	id, err := a.sessions.create(t.Context(), &session{Email: "jane@example.com"})
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
	st := newSessionStore(time.Hour, 0)
	id, err := st.create(t.Context(), &session{Email: "jane@example.com"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, ok := st.get(t.Context(), id); !ok {
		t.Fatal("fresh session should be readable")
	}

	// Expiry is enforced on read, not only by the cookie's max-age, so a client
	// that keeps presenting the id past the TTL still loses access.
	st.s[id].Expiry = time.Now().Add(-time.Second)
	if _, ok := st.get(t.Context(), id); ok {
		t.Error("expired session should not be readable")
	}
}

func TestRequireCSRF(t *testing.T) {
	a := &authenticator{
		sessions: newSessionStore(time.Hour, 0),
		admin: AdminConfig{
			Emails:      []string{"jane@example.com"},
			WriteEmails: []string{"jane@example.com"},
		},
		logger: testLogger(),
	}
	sess := &session{Email: "jane@example.com", CSRFToken: "the-real-token"}
	id, err := a.sessions.create(t.Context(), sess)
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
	a := &authenticator{
		sessions: newSessionStore(time.Hour, 0),
		admin: AdminConfig{
			Emails:      []string{"jane@example.com"},
			WriteEmails: []string{"jane@example.com"},
		},
		logger: testLogger(),
	}

	reached := false
	h := a.requireAdmin(a.requireWrite(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	post := func(canWrite bool) *httptest.ResponseRecorder {
		reached = false
		// Write permission comes from the configuration, not from the session:
		// the panel recomputes it on every request.
		a.admin.WriteEmails = nil
		if canWrite {
			a.admin.WriteEmails = []string{"jane@example.com"}
		}
		id, err := a.sessions.create(t.Context(), &session{Email: "jane@example.com"})
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
	a := &authenticator{
		sessions: newSessionStore(time.Hour, 0),
		admin: AdminConfig{
			Emails:      []string{"jane@example.com"},
			WriteEmails: []string{"jane@example.com"},
		},
		logger: testLogger(),
	}
	id, err := a.sessions.create(t.Context(), &session{Email: "jane@example.com", CanWrite: true, CSRFToken: "real"})
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

// A console left open on an unlocked laptop is the realistic threat, so a
// session that goes untouched has to die even though its absolute TTL is long.
func TestSessionIdleTimeout(t *testing.T) {
	st := newSessionStore(8*time.Hour, time.Hour)
	id, err := st.create(t.Context(), &session{Email: "jane@example.com"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, ok := st.get(t.Context(), id); !ok {
		t.Fatal("a fresh session should be readable")
	}

	// Reading it refreshes LastSeen, so an active session survives.
	st.s[id].LastSeen = time.Now().Add(-30 * time.Minute)
	if _, ok := st.get(t.Context(), id); !ok {
		t.Error("a session used half an hour ago should survive a one hour idle limit")
	}

	st.s[id].LastSeen = time.Now().Add(-90 * time.Minute)
	if _, ok := st.get(t.Context(), id); ok {
		t.Error("a session idle beyond the limit should be dropped")
	}
}

func TestSessionIdleTimeoutDisabled(t *testing.T) {
	st := newSessionStore(8*time.Hour, 0)
	id, _ := st.create(t.Context(), &session{Email: "jane@example.com"})
	st.s[id].LastSeen = time.Now().Add(-72 * time.Hour)
	if _, ok := st.get(t.Context(), id); !ok {
		t.Error("with the idle limit disabled, only the absolute TTL should end a session")
	}
}

// Destructive actions demand a recent login. Without this, deleting a connector
// eight hours into a session costs two clicks.
func TestRequireFreshAuth(t *testing.T) {
	st := newSessionStore(8*time.Hour, 0)
	a := &authenticator{
		sessions: st,
		admin: AdminConfig{
			Emails:      []string{"jane@example.com"},
			WriteEmails: []string{"jane@example.com"},
		},
		reauthWindow: 15 * time.Minute,
		logger:       testLogger(),
		oauth2:       oauth2.Config{ClientID: "dashboard", Endpoint: oauth2.Endpoint{AuthURL: "https://dex.example.com/auth"}},
		loginLimiter: newAttemptLimiter(10, time.Minute, nil),
	}

	run := func(method string, authAt time.Time) (*httptest.ResponseRecorder, bool) {
		reached := false
		h := a.requireAdmin(a.requireFreshAuth(func(w http.ResponseWriter, r *http.Request) {
			reached = true
		}))
		id, err := a.sessions.create(t.Context(), &session{Email: "jane@example.com", CanWrite: true})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		st.s[id].AuthAt = authAt

		req := httptest.NewRequest(method, "/clients/delete?id=x", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
		rr := httptest.NewRecorder()
		h(rr, req)
		return rr, reached
	}

	if _, reached := run(http.MethodGet, time.Now()); !reached {
		t.Error("a session that just authenticated should pass straight through")
	}

	rr, reached := run(http.MethodGet, time.Now().Add(-time.Hour))
	if reached {
		t.Error("a stale session must not reach a destructive handler")
	}
	if rr.Code != http.StatusFound {
		t.Errorf("stale GET: got %d, want a redirect to re-authenticate", rr.Code)
	}
	// prompt=login matters: without it dex would answer from its own session
	// and the check would prove nothing.
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "prompt=login") {
		t.Errorf("re-authentication redirect does not force a prompt: %s", loc)
	}

	rr, reached = run(http.MethodPost, time.Now().Add(-time.Hour))
	if reached || rr.Code != http.StatusForbidden {
		t.Errorf("stale POST: got %d (handler reached: %v), want %d", rr.Code, reached, http.StatusForbidden)
	}
}

// The post-login return path is attacker-controlled, so it gets the same
// treatment as the login form's "back" link.
func TestSanitizeNext(t *testing.T) {
	tests := map[string]string{
		"":                     "",
		"/clients":             "/clients",
		"/clients?id=x":        "/clients?id=x",
		"https://evil.example": "",
		"//evil.example":       "",
		"/\\evil.example":      "",
		"javascript:alert(1)":  "",
		"relative/path":        "",
	}
	for in, want := range tests {
		if got := sanitizeNext(in); got != want {
			t.Errorf("sanitizeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAttemptLimiter(t *testing.T) {
	l := newAttemptLimiter(3, time.Minute, nil)
	ctx := t.Context()

	for i := 0; i < 3; i++ {
		if !l.allow(ctx, "10.0.0.1") {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if l.allow(ctx, "10.0.0.1") {
		t.Error("the fourth attempt in the window should be refused")
	}
	// Another address has its own budget.
	if !l.allow(ctx, "10.0.0.2") {
		t.Error("a different address should not be affected")
	}

	var nilLimiter *attemptLimiter
	if !nilLimiter.allow(ctx, "10.0.0.1") {
		t.Error("a nil limiter must allow everything rather than panic")
	}
}

// Two dashboard replicas must share one login budget. Without this, replicating
// the panel multiplies its throttle by the number of instances -- the same hole
// the shared state closed in dex.
func TestTwoDashboardsShareOneLoginBudget(t *testing.T) {
	m := miniredis.RunT(t)

	newLimiter := func() *attemptLimiter {
		c, err := dexvalkey.New(t.Context(), dexvalkey.Config{
			Addresses: []string{m.Addr()}, KeyPrefix: "dex-dashboard:",
		})
		if err != nil {
			t.Fatalf("valkey client: %v", err)
		}
		t.Cleanup(c.Close)
		return newAttemptLimiter(2, time.Minute, c)
	}

	a, b := newLimiter(), newLimiter()

	if !a.allow(t.Context(), "10.0.0.1") || !b.allow(t.Context(), "10.0.0.1") {
		t.Fatal("the shared budget refused attempts inside the limit")
	}
	if a.allow(t.Context(), "10.0.0.1") {
		t.Error("the third attempt got through: each replica is counting on its own")
	}
}

// Without a shared store nothing changes: one process, its own map.
func TestTheLocalLimiterStillWorks(t *testing.T) {
	l := newAttemptLimiter(2, time.Minute, nil)
	ctx := t.Context()

	if !l.allow(ctx, "10.0.0.1") || !l.allow(ctx, "10.0.0.1") {
		t.Fatal("the local limiter refused attempts inside the limit")
	}
	if l.allow(ctx, "10.0.0.1") {
		t.Error("the local limiter stopped limiting")
	}
}

// A Valkey that stopped answering must not turn the throttle off. It degrades to
// one replica's worth of limit, never to none.
func TestTheDashboardLimiterFallsBackWhenValkeyIsDown(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{
		Addresses: []string{m.Addr()}, KeyPrefix: "dex-dashboard:",
	})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)

	l := newAttemptLimiter(2, time.Minute, c)
	m.Close()

	ctx := t.Context()
	if !l.allow(ctx, "10.0.0.1") || !l.allow(ctx, "10.0.0.1") {
		t.Fatal("the fallback refused attempts inside the limit")
	}
	if l.allow(ctx, "10.0.0.1") {
		t.Error("with Valkey down the dashboard stopped limiting logins")
	}
}

// The __Host- prefix only works over HTTPS, so it must appear there and nowhere
// else: an invalid prefix over plain HTTP would have the browser drop the cookie.
func TestHostCookiePrefix(t *testing.T) {
	secure := &authenticator{secure: true}
	if got := secure.cookie(sessionCookie, "v", 60).Name; got != "__Host-"+sessionCookie {
		t.Errorf("over HTTPS the cookie name is %q, want the __Host- prefix", got)
	}
	plain := &authenticator{secure: false}
	if got := plain.cookie(sessionCookie, "v", 60).Name; got != sessionCookie {
		t.Errorf("over HTTP the cookie name is %q, want no prefix", got)
	}
}

// Permission is decided from the configuration on every request, not from what
// was stored when the administrator signed in. With the sessions in Valkey they
// outlive a restart of the panel, so this is the only thing that makes taking
// someone out of admin.writeGroups take effect before their session expires.
func TestPermissionIsRecomputedPerRequest(t *testing.T) {
	a := &authenticator{
		sessions: newSessionStore(time.Hour, 0),
		admin: AdminConfig{
			Groups:      []string{"dex-admins"},
			WriteGroups: []string{"dex-writers"},
		},
		logger: testLogger(),
	}

	id, err := a.sessions.create(t.Context(), &session{
		Email:  "jane@example.com",
		Groups: []string{"dex-admins", "dex-writers"},
		// Deliberately wrong: whatever is stored here must not decide anything.
		CanWrite: false,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	post := func() (*httptest.ResponseRecorder, bool) {
		reached := false
		h := a.requireAdmin(a.requireWrite(func(w http.ResponseWriter, r *http.Request) {
			reached = true
		}))
		req := httptest.NewRequest(http.MethodPost, "/clients/delete", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
		rr := httptest.NewRecorder()
		h(rr, req)
		return rr, reached
	}

	if _, reached := post(); !reached {
		t.Fatal("a member of a write group should reach the handler, whatever the session recorded")
	}

	// Demoted while signed in: the very next request loses write permission.
	a.admin.WriteGroups = nil
	rr, reached := post()
	if reached {
		t.Error("a demoted administrator still reached a write handler")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("demoted administrator: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	// Access revoked entirely: the session is destroyed, not left to expire.
	a.admin.Groups = nil
	if _, reached := post(); reached {
		t.Error("an administrator with no access left still reached a handler")
	}
	if _, ok := a.sessions.get(t.Context(), id); ok {
		t.Error("the session of a revoked administrator should have been dropped, not left to expire")
	}
}

// The throttle key must not be chosen by whoever is being throttled. Since the
// counters moved to a shared Valkey, one unauthenticated request per made-up
// forwarded address is one more key in a store that runs with noeviction: fill
// it and no administrator session can be created at all.
func TestClientAddrIgnoresTheForwardedHeaderByDefault(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:52020"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")

	if got := clientAddr(r, false); got != "10.0.0.1" {
		t.Errorf("clientAddr = %q, want the address the connection came from, 10.0.0.1", got)
	}
}

// Behind the load balancer this deployment is designed around, ignoring the
// header would be the other failure: every administrator in one bucket. The
// operator says which topology it is, and then the first hop is the client.
func TestClientAddrHonorsTheForwardedHeaderWhenTrusted(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:52020"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")

	if got := clientAddr(r, true); got != "203.0.113.9" {
		t.Errorf("clientAddr = %q, want the first hop of the header, 203.0.113.9", got)
	}
}
