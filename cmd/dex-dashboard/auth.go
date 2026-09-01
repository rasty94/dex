package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	sessionCookie = "dex_dashboard_session"
	stateCookie   = "dex_dashboard_state"
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
}

// sessionStore keeps sessions in memory.
//
// ponytail: in-process, so sessions are dropped on restart and do not survive
// more than one replica. For a single admin panel that is the right trade:
// nothing to encrypt, nothing to rotate, and a restart just asks for a fresh
// login. Move to a shared store only when the panel is actually replicated.
type sessionStore struct {
	ttl time.Duration
	mu  sync.Mutex
	s   map[string]*session
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, s: make(map[string]*session)}
}

func (st *sessionStore) create(sess *session) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	sess.Expiry = time.Now().Add(st.ttl)

	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweep()
	st.s[id] = sess
	return id, nil
}

func (st *sessionStore) get(id string) (*session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	sess, ok := st.s[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(sess.Expiry) {
		delete(st.s, id)
		return nil, false
	}
	return sess, true
}

func (st *sessionStore) delete(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.s, id)
}

// sweep drops expired sessions. Callers must hold st.mu.
func (st *sessionStore) sweep() {
	now := time.Now()
	for id, sess := range st.s {
		if now.After(sess.Expiry) {
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

// authenticator runs the OIDC login against dex and decides who is an admin.
type authenticator struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   oauth2.Config
	sessions *sessionStore
	admin    AdminConfig
	secure   bool
	logger   *slog.Logger
}

func newAuthenticator(ctx context.Context, c *Config, logger *slog.Logger) (*authenticator, error) {
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
		sessions: newSessionStore(c.Admin.SessionTTL),
		admin:    c.Admin,
		secure:   strings.HasPrefix(c.BaseURL, "https://"),
		logger:   logger,
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

func (a *authenticator) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, a.oauth2.AuthCodeURL(state), http.StatusFound)
}

func (a *authenticator) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		a.logger.Error("authorization error from dex", "error", errMsg, "description", r.URL.Query().Get("error_description"))
		http.Error(w, "Authentication failed.", http.StatusUnauthorized)
		return
	}

	want, err := r.Cookie(stateCookie)
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
	id, err := a.sessions.create(&session{
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
	http.SetCookie(w, a.cookie(sessionCookie, id, int(a.sessions.ttl/time.Second)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.sessions.delete(c.Value)
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
		c, err := r.Cookie(sessionCookie)
		if err == nil {
			if sess, ok := a.sessions.get(c.Value); ok {
				next(w, r.WithContext(context.WithValue(r.Context(), sessionCtxKey, sess)))
				return
			}
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Session expired. Reload and sign in again.", http.StatusUnauthorized)
			return
		}
		a.handleLogin(w, r)
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
