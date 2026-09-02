package main

import (
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	api "github.com/dexidp/dex/api/v2"
	"github.com/dexidp/dex/server/tokens"
)

//go:embed templates/*.html
var templateFS embed.FS

// dashboard serves the read-only views. Every handler is wrapped by
// requireAdmin, so a nil session here is a programming error, not a state to
// render around.
type dashboard struct {
	dex       *dexClient
	auth      *authenticator
	telemetry *telemetry
	pages     map[string]*template.Template
	logger    *slog.Logger
}

// pageTemplates are the views, each paired with the shared layout.
var pageTemplates = []string{
	"overview.html", "clients.html", "connectors.html", "users.html", "sessions.html",
	"client_form.html", "user_form.html", "connector_form.html", "confirm.html", "status.html", "discovery.html",
}

var templateFuncs = template.FuncMap{
	"join": strings.Join,
	"ts": func(sec int64) string {
		if sec == 0 {
			return "—"
		}
		return time.Unix(sec, 0).UTC().Format("2006-01-02 15:04 UTC")
	},
}

// newDashboard parses each page into its own template set rather than parsing
// the whole directory at once. Every page defines a block named "content", and
// in a single set those definitions overwrite each other — the last file parsed
// would win and every page would render the same body.
func newDashboard(dex *dexClient, auth *authenticator, tel *telemetry, logger *slog.Logger) (*dashboard, error) {
	pages := make(map[string]*template.Template, len(pageTemplates))
	for _, name := range pageTemplates {
		t, err := template.New(name).Funcs(templateFuncs).
			ParseFS(templateFS, "templates/layout.html", "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		pages[name] = t
	}
	return &dashboard{dex: dex, auth: auth, telemetry: tel, pages: pages, logger: logger}, nil
}

// page is what every template receives.
type page struct {
	Title   string
	Nav     string
	Session *session
	Error   string
	// Notice carries the one-line result of a write, handed over in the query
	// string by the redirect that followed it.
	Notice string
	// Filter is the current ?q=, so the search box keeps what was typed and the
	// listing can say how many rows it hid.
	Filter string
	Total  int
	Shown  int
	// SelfPath is this listing's own path, so "Clear" can drop the filter
	// without the template having to know which page it is on.
	SelfPath string
	Data     any
}

func (d *dashboard) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	p.Session = sessionFrom(r.Context())
	if p.Notice == "" {
		p.Notice = r.URL.Query().Get("msg")
	}
	if p.Filter == "" {
		p.Filter = filterQuery(r)
	}
	if p.SelfPath == "" {
		p.SelfPath = r.URL.Path
	}
	t, ok := d.pages[name]
	if !ok {
		d.logger.Error("unknown template", "template", name)
		http.Error(w, "Internal error.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, p); err != nil {
		// The response is already partly written by now, so there is nothing
		// useful left to send the browser; log it and move on.
		d.logger.Error("template error", "template", name, "err", err)
	}
}

// renderList is the shape every read-only view shares: fetch, and on failure
// render the page with the error instead of a blank 500. An admin panel that
// says which call failed beats one that says "Internal Server Error".
// The variadic errMsg lets a failed write re-render its listing with the reason,
// instead of leaving the operator on a bare error page with no way back.
func (d *dashboard) renderList(w http.ResponseWriter, r *http.Request, name, title, nav string, fetch func() (any, error), errMsg ...string) {
	msg := ""
	if len(errMsg) > 0 {
		msg = errMsg[0]
	}
	data, err := fetch()
	if err != nil {
		d.logger.Error("dex API call failed", "view", nav, "err", err)
		d.render(w, r, name, page{Title: title, Nav: nav, Error: friendlyGRPCError(err)})
		return
	}
	d.render(w, r, name, page{Title: title, Nav: nav, Error: msg, Data: data})
}

// friendlyGRPCError turns the common failures into something an operator can
// act on. The rest is passed through verbatim.
func friendlyGRPCError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "authorization token is not provided"),
		strings.Contains(msg, "invalid authorization token"):
		return "dex refused the API token. Check dex.token in the dashboard config against the gRPC API's token."
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "Unavailable"):
		return "Cannot reach dex's gRPC API. Check dex.grpcAddress and that the API is enabled."
	case strings.Contains(msg, "api_connectors_crud"):
		return "Connector listing is gated behind dex's api_connectors_crud feature flag, which is off."
	case strings.Contains(msg, "api_sessions_identities_crud"):
		return "Browser sessions are gated behind dex's api_sessions_identities_crud feature flag, which is off."
	case strings.Contains(msg, "sessions_enabled"), strings.Contains(msg, "sessions are not enabled"):
		return "dex has browser sessions turned off (sessions_enabled). Refresh tokens are still listed below."
	case strings.Contains(msg, "wire-format"), strings.Contains(msg, "cannot parse invalid"):
		return "That is not a valid subject. Copy the sub claim from the user's ID token verbatim."
	}
	return msg
}

func (d *dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	d.renderList(w, r, "overview.html", "Overview", "overview", func() (any, error) {
		version, err := d.dex.version(r.Context())
		if err != nil {
			return nil, err
		}
		clients, err := d.dex.listClients(r.Context())
		if err != nil {
			return nil, err
		}
		passwords, err := d.dex.listPasswords(r.Context())
		if err != nil {
			return nil, err
		}
		// Connectors are behind a feature flag, so a failure here is reported as
		// "unavailable" rather than sinking the whole overview.
		connectorCount := -1
		if connectors, err := d.dex.listConnectors(r.Context()); err == nil {
			connectorCount = len(connectors)
		}
		return struct {
			Version        *api.VersionResp
			Clients        int
			LocalUsers     int
			ConnectorCount int
		}{version, len(clients), len(passwords), connectorCount}, nil
	})
}

func (d *dashboard) handleClients(w http.ResponseWriter, r *http.Request) {
	q := filterQuery(r)
	d.renderFiltered(w, r, "clients.html", "Clients", "clients", func() (any, int, error) {
		all, err := d.dex.listClients(r.Context())
		if err != nil {
			return nil, 0, err
		}
		out := make([]*api.ClientInfo, 0, len(all))
		for _, c := range all {
			if matchesFilter(q, c.Id, c.Name, strings.Join(c.RedirectUris, " ")) {
				out = append(out, c)
			}
		}
		return out, len(all), nil
	})
}

func (d *dashboard) handleConnectors(w http.ResponseWriter, r *http.Request) {
	q := filterQuery(r)
	d.renderFiltered(w, r, "connectors.html", "Connectors", "connectors", func() (any, int, error) {
		all, err := d.dex.listConnectors(r.Context())
		if err != nil {
			return nil, 0, err
		}
		out := make([]*api.Connector, 0, len(all))
		for _, c := range all {
			if matchesFilter(q, c.Id, c.Type, c.Name) {
				out = append(out, c)
			}
		}
		return out, len(all), nil
	})
}

func (d *dashboard) handleUsers(w http.ResponseWriter, r *http.Request) {
	q := filterQuery(r)
	d.renderFiltered(w, r, "users.html", "Local users", "users", func() (any, int, error) {
		all, err := d.dex.listPasswords(r.Context())
		if err != nil {
			return nil, 0, err
		}
		out := make([]*api.Password, 0, len(all))
		for _, p := range all {
			if matchesFilter(q, p.Email, p.Username, p.UserId) {
				out = append(out, p)
			}
		}
		return out, len(all), nil
	})
}

// renderFiltered is renderList for a listing that can be narrowed by ?q=. The
// fetch reports how many rows existed before filtering, so the page can say
// what it is hiding instead of looking empty.
func (d *dashboard) renderFiltered(w http.ResponseWriter, r *http.Request, name, title, nav string, fetch func() (any, int, error)) {
	data, total, err := fetch()
	if err != nil {
		d.logger.Error("dex API call failed", "view", nav, "err", err)
		d.render(w, r, name, page{Title: title, Nav: nav, Error: friendlyGRPCError(err)})
		return
	}
	shown := 0
	if v, ok := data.([]*api.ClientInfo); ok {
		shown = len(v)
	} else if v, ok := data.([]*api.Connector); ok {
		shown = len(v)
	} else if v, ok := data.([]*api.Password); ok {
		shown = len(v)
	}
	d.render(w, r, name, page{Title: title, Nav: nav, Total: total, Shown: shown, Data: data})
}

// handleSessions looks up a user's refresh tokens. dex keys them on the "sub"
// claim, which encodes (userID, connectorID), so the form offers both ways in:
// paste a sub straight from a token, or give the two halves an operator
// normally has and let the dashboard build it.
func (d *dashboard) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sub := strings.TrimSpace(q.Get("sub"))
	userID := strings.TrimSpace(q.Get("user_id"))
	connID := strings.TrimSpace(q.Get("conn_id"))

	data := sessionsData{Subject: sub, UserID: userID, ConnID: connID}
	if conns, err := d.dex.listConnectors(r.Context()); err == nil {
		for _, c := range conns {
			data.Connectors = append(data.Connectors, c.Id)
		}
	}
	// The local password DB is always available as a connector id, even when
	// listing connectors is gated behind the feature flag.
	if !slices.Contains(data.Connectors, "local") {
		data.Connectors = append(data.Connectors, "local")
	}
	sort.Strings(data.Connectors)

	render := func(p page) {
		p.Data = data
		d.render(w, r, "sessions.html", p)
	}

	switch {
	case sub == "" && userID != "" && connID != "":
		encoded, err := tokens.GenSubject(userID, connID)
		if err != nil {
			d.logger.Error("failed to encode subject", "err", err)
			render(page{Title: "Sessions", Nav: "sessions", Error: "Could not build a subject from that user and connector."})
			return
		}
		sub = encoded
		data.Subject = sub
	case sub != "" && (userID == "" || connID == ""):
		// Browser sessions are keyed on the pair, not on the subject, so a pasted
		// sub has to be taken apart before they can be listed. A sub that does not
		// decode is not an error here: the refresh token lookup below still works
		// with it, and that is what the user asked for.
		if u, c, err := tokens.ParseSubject(sub); err == nil {
			userID, connID = u, c
		}
	}

	if sub == "" {
		render(page{Title: "Sessions", Nav: "sessions"})
		return
	}

	tokens, err := d.dex.listRefresh(r.Context(), sub)
	if err != nil {
		// Searched stays false so the page shows the error alone, rather than an
		// empty result table next to it saying the user has no sessions.
		d.logger.Error("failed to list refresh tokens", "err", err)
		render(page{Title: "Sessions", Nav: "sessions", Error: friendlyGRPCError(err)})
		return
	}
	data.Tokens = tokens
	data.Searched = true

	// Browser sessions and the identity are separate lookups behind the same
	// feature flag, so a failure in either must not hide the refresh tokens that
	// were found. The identity is best-effort: a user who has never signed in
	// since dex started recording identities simply has none.
	if userID != "" && connID != "" {
		sessions, err := d.dex.listAuthSessions(r.Context(), userID, connID)
		if err != nil {
			d.logger.Info("could not list auth sessions", "err", err)
			data.SessionsUnavailable = friendlyGRPCError(err)
		} else {
			data.AuthSessions = sessions
		}

		if identity, err := d.dex.getUserIdentity(r.Context(), userID, connID); err == nil {
			data.Identity = identity
		} else {
			d.logger.Info("could not get user identity", "err", err)
		}
	}

	render(page{Title: "Sessions", Nav: "sessions"})
}

// sessionsData drives the sessions view.
type sessionsData struct {
	Subject    string
	UserID     string
	ConnID     string
	Connectors []string
	Tokens     []*api.RefreshTokenRef
	Searched   bool

	// AuthSessions are the user's signed-in browsers. They are a different thing
	// from the refresh tokens above — one browser versus one application's grant
	// — and only exist when dex has sessions enabled.
	AuthSessions []*api.AuthSession
	// SessionsUnavailable explains why the browser-session table is missing,
	// which is nearly always a feature flag rather than a real failure.
	SessionsUnavailable string

	// Identity is what dex knows about this user on this connector: the claims
	// it last saw, what they consented to, their second factors, and whether the
	// account is locked out. Nil when it could not be fetched.
	Identity *api.UserIdentity
}

// filterQuery is the ?q= a listing was filtered by.
func filterQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("q"))
}

// matchesFilter reports whether any of the fields contains q, case-insensitively.
// Filtering happens here rather than in dex because the API has no search: the
// whole list is fetched either way, and an operator hunting for one client
// should not have to read a hundred rows to find it.
func matchesFilter(q string, fields ...string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}
