package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

const (
	sessionCookie = "dex_dashboard_session"
	stateCookie   = "dex_dashboard_state"
	nextCookie    = "dex_dashboard_next"
)

// session is one signed-in administrator. It never holds the dex admin token:
// that lives in the process, and the browser only ever carries an opaque id.
type session struct {
	Email     string
	Name      string
	Groups    []string
	CanWrite  bool
	CSRFToken string
	Expiry    time.Time

	// AuthAt is when the administrator last proved who they are. Destructive
	// actions check it, so a session that has been open all day cannot delete a
	// connector without a fresh login.
	AuthAt time.Time
	// LastSeen backs the idle timeout.
	LastSeen time.Time
}

// sessionStore keeps sessions in memory.
//
// ponytail: in-process, so sessions are dropped on restart and do not survive
// more than one replica. For a single admin panel that is the right trade:
// nothing to encrypt, nothing to rotate, and a restart just asks for a fresh
// login. Move to a shared store only when the panel is actually replicated.
type sessionStore struct {
	ttl     time.Duration
	idleTTL time.Duration
	mu      sync.Mutex
	s       map[string]*session
}

func newSessionStore(ttl, idleTTL time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, idleTTL: idleTTL, s: make(map[string]*session)}
}

func (st *sessionStore) create(_ context.Context, sess *session) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	sess.Expiry = now.Add(st.ttl)
	sess.AuthAt = now
	sess.LastSeen = now

	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweep()
	st.s[id] = sess
	return id, nil
}

func (st *sessionStore) get(_ context.Context, id string) (*session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	sess, ok := st.s[id]
	if !ok {
		return nil, false
	}
	now := time.Now()
	// Both limits are enforced on read, not just by the cookie's max-age: a
	// client that keeps presenting the id has to lose access too.
	if now.After(sess.Expiry) || st.idle(sess, now) {
		delete(st.s, id)
		return nil, false
	}
	sess.LastSeen = now
	return sess, true
}

func (st *sessionStore) idle(sess *session, now time.Time) bool {
	return st.idleTTL > 0 && now.Sub(sess.LastSeen) > st.idleTTL
}

func (st *sessionStore) delete(_ context.Context, id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.s, id)
}

// sweep drops expired sessions. Callers must hold st.mu.
func (st *sessionStore) sweep() {
	now := time.Now()
	for id, sess := range st.s {
		if now.After(sess.Expiry) || st.idle(sess, now) {
			delete(st.s, id)
		}
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sessions is what the panel needs from a session store: the in-process one and
// the shared one both satisfy it.
type sessions interface {
	create(ctx context.Context, sess *session) (string, error)
	get(ctx context.Context, id string) (*session, bool)
	delete(ctx context.Context, id string)
}

// sessionsFor picks where administrator sessions live. Without a valkey address
// they stay in this process, which is what a single panel wants: nothing to
// encrypt, nothing to rotate, and a restart just asks for a fresh login.
func sessionsFor(c *Config, vk *dexvalkey.Client, logger *slog.Logger) sessions {
	if vk != nil {
		return newValkeySessions(vk, c.Admin.SessionTTL, c.Admin.IdleTTL, logger)
	}
	return newSessionStore(c.Admin.SessionTTL, c.Admin.IdleTTL)
}

// authenticator runs the OIDC login against dex and decides who is an admin.
type authenticator struct {
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2       oauth2.Config
	sessions     sessions
	admin        AdminConfig
	secure       bool
	reauthWindow time.Duration
	loginLimiter *attemptLimiter
	logger       *slog.Logger
}

func newAuthenticator(ctx context.Context, c *Config, vk *dexvalkey.Client, logger *slog.Logger) (*authenticator, error) {
	provider, err := oidc.NewProvider(ctx, c.OIDC.Issuer)
	if err != nil {
		return nil, fmt.Errorf("query dex's OIDC discovery endpoint: %w", err)
	}

	// "groups" is not optional here: the authorization gate reads it. Asking for
	// it explicitly avoids a panel that authenticates fine and then locks
	// everyone out because the claim never arrived.
	scopes := c.OIDC.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if !slices.Contains(scopes, "groups") {
		scopes = append(scopes, "groups")
	}

	return &authenticator{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: c.OIDC.ClientID}),
		oauth2: oauth2.Config{
			ClientID:     c.OIDC.ClientID,
			ClientSecret: c.OIDC.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  strings.TrimSuffix(c.BaseURL, "/") + "/callback",
			Scopes:       scopes,
		},
		sessions:     sessionsFor(c, vk, logger),
		admin:        c.Admin,
		secure:       strings.HasPrefix(c.BaseURL, "https://"),
		reauthWindow: c.Admin.ReauthWindow,
		loginLimiter: newAttemptLimiter(10, time.Minute),
		logger:       logger,
	}, nil
}

// access reports what these claims may do: get in at all, and change anything
// once in. Write permission is granted separately, never implied by read.
func (a *authenticator) access(email string, groups []string) (canRead, canWrite bool) {
	canRead = matches(email, groups, a.admin.Emails, a.admin.Groups)
	canWrite = canRead && matches(email, groups, a.admin.WriteEmails, a.admin.WriteGroups)
	return canRead, canWrite
}

// matches reports whether the identity is named by either allow list. Emails
// compare case-insensitively; group names must match exactly, so "dex-admins"
// does not admit "dex-admins-readonly".
func matches(email string, groups, allowedEmails, allowedGroups []string) bool {
	if email != "" {
		for _, allowed := range allowedEmails {
			if strings.EqualFold(allowed, email) {
				return true
			}
		}
	}
	for _, allowed := range allowedGroups {
		if allowed != "" && slices.Contains(groups, allowed) {
			return true
		}
	}
	return false
}

// cookieName applies the __Host- prefix when the panel is served over HTTPS.
// Browsers refuse such a cookie unless it is Secure, host-only and Path=/, which
// stops a sibling subdomain from planting one — the shape of attack a bare name
// cannot defend against. Over plain HTTP the prefix is invalid, so it is only
// used where it works.
func (a *authenticator) cookieName(name string) string {
	if a.secure {
		return "__Host-" + name
	}
	return name
}

func (a *authenticator) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     a.cookieName(name),
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// readCookie finds a cookie under whichever name this deployment uses.
func (a *authenticator) readCookie(r *http.Request, name string) (*http.Cookie, error) {
	return r.Cookie(a.cookieName(name))
}

// startLogin sends the browser to dex. next is where to return afterwards, and
// forceReauth asks dex to prompt even if the user already has a session there —
// without it, a re-authentication check would be satisfied by a silent redirect
// and prove nothing.
func (a *authenticator) startLogin(w http.ResponseWriter, r *http.Request, next string, forceReauth bool) {
	if !a.loginLimiter.allow(clientAddr(r)) {
		a.logger.Warn("login rate limit exceeded", "addr", clientAddr(r))
		http.Error(w, "Too many login attempts. Try again in a minute.", http.StatusTooManyRequests)
		return
	}

	state, err := randomToken()
	if err != nil {
		a.logger.Error("failed to generate login state", "err", err)
		http.Error(w, "Login error.", http.StatusInternalServerError)
		return
	}

	// The state is echoed back by the provider and compared with the cookie, so
	// a login response the browser did not ask for is rejected (CSRF on the
	// callback).
	http.SetCookie(w, a.cookie(stateCookie, state, int(10*time.Minute/time.Second)))
	// Where to come back to. Sanitized the same way as the login form's "back"
	// link: an unchecked return path is an open redirect.
	http.SetCookie(w, a.cookie(nextCookie, sanitizeNext(next), int(10*time.Minute/time.Second)))

	opts := []oauth2.AuthCodeOption{}
	if forceReauth {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"))
	}
	http.Redirect(w, r, a.oauth2.AuthCodeURL(state, opts...), http.StatusFound)
}

// sanitizeNext permits only a same-origin absolute path as the post-login
// destination. An absolute URL, a scheme-relative "//host" or a "/\host" that
// browsers treat as protocol-relative would turn the login into an open
// redirect.
func sanitizeNext(next string) string {
	if next == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	if next[0] != '/' {
		return ""
	}
	if len(next) >= 2 && (next[1] == '/' || next[1] == '\\') {
		return ""
	}
	return next
}

func (a *authenticator) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		a.logger.Error("authorization error from dex", "error", errMsg, "description", r.URL.Query().Get("error_description"))
		http.Error(w, "Authentication failed.", http.StatusUnauthorized)
		return
	}

	want, err := a.readCookie(r, stateCookie)
	if err != nil {
		http.Error(w, "Login session expired. Try again.", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, a.cookie(stateCookie, "", -1))

	got := r.URL.Query().Get("state")
	if subtle.ConstantTimeCompare([]byte(got), []byte(want.Value)) != 1 {
		http.Error(w, "Invalid login state.", http.StatusBadRequest)
		return
	}

	token, err := a.oauth2.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		a.logger.Error("failed to exchange authorization code", "err", err)
		http.Error(w, "Authentication failed.", http.StatusUnauthorized)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		a.logger.Error("no id_token in dex's token response")
		http.Error(w, "Authentication failed.", http.StatusUnauthorized)
		return
	}
	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		a.logger.Error("failed to verify ID token", "err", err)
		http.Error(w, "Authentication failed.", http.StatusUnauthorized)
		return
	}

	var claims struct {
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		a.logger.Error("failed to decode ID token claims", "err", err)
		http.Error(w, "Authentication failed.", http.StatusUnauthorized)
		return
	}

	canRead, canWrite := a.access(claims.Email, claims.Groups)
	if !canRead {
		// Logged with the identity, because a refused administrative login is
		// exactly the event someone will go looking for afterwards.
		a.logger.Warn("refused dashboard access", "email", claims.Email, "groups", claims.Groups)
		http.Error(w, "You are not authorized to use this dashboard.", http.StatusForbidden)
		return
	}

	csrf, err := randomToken()
	if err != nil {
		a.logger.Error("failed to generate CSRF token", "err", err)
		http.Error(w, "Login error.", http.StatusInternalServerError)
		return
	}
	id, err := a.sessions.create(ctx, &session{
		Email:     claims.Email,
		Name:      claims.Name,
		Groups:    claims.Groups,
		CanWrite:  canWrite,
		CSRFToken: csrf,
	})
	if err != nil {
		a.logger.Error("failed to create session", "err", err)
		http.Error(w, "Login error.", http.StatusInternalServerError)
		return
	}

	a.logger.Info("dashboard login", "email", claims.Email, "can_write", canWrite)
	http.SetCookie(w, a.cookie(sessionCookie, id, int(a.admin.SessionTTL/time.Second)))

	next := "/"
	if c, err := a.readCookie(r, nextCookie); err == nil {
		if clean := sanitizeNext(c.Value); clean != "" {
			next = clean
		}
		http.SetCookie(w, a.cookie(nextCookie, "", -1))
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := a.readCookie(r, sessionCookie); err == nil {
		a.sessions.delete(r.Context(), c.Value)
	}
	http.SetCookie(w, a.cookie(sessionCookie, "", -1))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type ctxKey string

const sessionCtxKey ctxKey = "session"

func sessionFrom(ctx context.Context) *session {
	sess, _ := ctx.Value(sessionCtxKey).(*session)
	return sess
}

// requireAdmin gates every page behind a valid session. Unauthenticated GETs
// start a login; anything else is refused outright rather than redirected, so a
// stale form post cannot be replayed into a fresh login.
func (a *authenticator) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := a.readCookie(r, sessionCookie)
		if err == nil {
			if sess, ok := a.sessions.get(r.Context(), c.Value); ok {
				next(w, r.WithContext(context.WithValue(r.Context(), sessionCtxKey, sess)))
				return
			}
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Session expired. Reload and sign in again.", http.StatusUnauthorized)
			return
		}
		a.startLogin(w, r, r.URL.RequestURI(), false)
	}
}

// requireWrite gates every state-changing route. It sits behind requireCSRF, so
// a request has to be authenticated, hold the right token and carry write
// permission before anything happens.
func (a *authenticator) requireWrite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess == nil || !sess.CanWrite {
			email := ""
			if sess != nil {
				email = sess.Email
			}
			a.logger.Warn("refused write without permission", "email", email, "path", r.URL.Path)
			http.Error(w, "Your account has read-only access to this dashboard.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// requireCSRF protects mutating requests. Phase one is read-only apart from
// logout, but the check lands with the first form rather than after it.
func (a *authenticator) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess == nil {
			http.Error(w, "Session expired.", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.FormValue("csrf_token")), []byte(sess.CSRFToken)) != 1 {
			a.logger.Warn("rejected request with bad CSRF token", "email", sess.Email, "path", r.URL.Path)
			http.Error(w, "Invalid request token. Reload the page and try again.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// attemptLimiter throttles how often one address may start a login. Without it
// a broken client, or someone probing, can drive an unbounded number of
// authorization redirects and token exchanges at dex.
//
// ponytail: in-process and keyed by address only, matching how the sessions are
// stored. It is a brake on noise, not a defense against a botnet.
type attemptLimiter struct {
	limit  int
	window time.Duration

	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newAttemptLimiter(limit int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{limit: limit, window: window, attempts: map[string][]time.Time{}}
}

// allow records an attempt from key and reports whether it may proceed.
func (l *attemptLimiter) allow(key string) bool {
	if l == nil {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Sweep every key, not just this one, so addresses that stop calling do not
	// sit in the map forever.
	for k, times := range l.attempts {
		kept := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.attempts, k)
			continue
		}
		l.attempts[k] = kept
	}

	if len(l.attempts[key]) >= l.limit {
		return false
	}
	l.attempts[key] = append(l.attempts[key], now)
	return true
}

// clientAddr identifies the caller for rate limiting. The dashboard sits behind
// a proxy in every real deployment, so a forwarded address is used when present
// — it is a brake on accidents, and treating it as authoritative is not the
// point.
func clientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, found := strings.Cut(xff, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// needsReauth reports whether this session authenticated too long ago to be
// trusted with a destructive action.
func (a *authenticator) needsReauth(sess *session) bool {
	return a.reauthWindow > 0 && time.Since(sess.AuthAt) > a.reauthWindow
}

// requireFreshAuth guards destructive routes. A stale session is sent back
// through dex with prompt=login and returned to where it was going, so the
// action is not lost — only delayed by proving who you are.
func (a *authenticator) requireFreshAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess == nil || !a.needsReauth(sess) {
			next(w, r)
			return
		}

		if r.Method != http.MethodGet {
			// A POST cannot be replayed after a login round trip without
			// resubmitting its body, so it is refused with an explanation
			// rather than silently redirected.
			http.Error(w, "Your session is too old for this action. Reload the page and sign in again.", http.StatusForbidden)
			return
		}

		a.logger.Info("re-authentication required", "email", sess.Email, "path", r.URL.Path)
		a.startLogin(w, r, r.URL.RequestURI(), true)
	}
}
