package main

import (
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	api "github.com/dexidp/dex/api/v2"
)

//go:embed templates/*.html
var templateFS embed.FS

// dashboard serves the read-only views. Every handler is wrapped by
// requireAdmin, so a nil session here is a programming error, not a state to
// render around.
type dashboard struct {
	dex    *dexClient
	auth   *authenticator
	pages  map[string]*template.Template
	logger *slog.Logger
}

// pageTemplates are the views, each paired with the shared layout.
var pageTemplates = []string{
	"overview.html", "clients.html", "connectors.html", "users.html", "sessions.html",
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
func newDashboard(dex *dexClient, auth *authenticator, logger *slog.Logger) (*dashboard, error) {
	pages := make(map[string]*template.Template, len(pageTemplates))
	for _, name := range pageTemplates {
		t, err := template.New(name).Funcs(templateFuncs).
			ParseFS(templateFS, "templates/layout.html", "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		pages[name] = t
	}
	return &dashboard{dex: dex, auth: auth, pages: pages, logger: logger}, nil
}

// page is what every template receives.
type page struct {
	Title   string
	Nav     string
	Session *session
	Error   string
	Data    any
}

func (d *dashboard) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	p.Session = sessionFrom(r.Context())
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
func (d *dashboard) renderList(w http.ResponseWriter, r *http.Request, name, title, nav string, fetch func() (any, error)) {
	data, err := fetch()
	if err != nil {
		d.logger.Error("dex API call failed", "view", nav, "err", err)
		d.render(w, r, name, page{Title: title, Nav: nav, Error: friendlyGRPCError(err)})
		return
	}
	d.render(w, r, name, page{Title: title, Nav: nav, Data: data})
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
	d.renderList(w, r, "clients.html", "Clients", "clients", func() (any, error) {
		return d.dex.listClients(r.Context())
	})
}

func (d *dashboard) handleConnectors(w http.ResponseWriter, r *http.Request) {
	d.renderList(w, r, "connectors.html", "Connectors", "connectors", func() (any, error) {
		return d.dex.listConnectors(r.Context())
	})
}

func (d *dashboard) handleUsers(w http.ResponseWriter, r *http.Request) {
	d.renderList(w, r, "users.html", "Local users", "users", func() (any, error) {
		return d.dex.listPasswords(r.Context())
	})
}

// handleSessions looks up a user's refresh tokens by subject. The dex API keys
// on the "sub" claim, so the form asks for one instead of guessing.
func (d *dashboard) handleSessions(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimSpace(r.URL.Query().Get("sub"))
	data := struct {
		Subject  string
		Tokens   []*api.RefreshTokenRef
		Searched bool
	}{Subject: sub}

	if sub == "" {
		d.render(w, r, "sessions.html", page{Title: "Sessions", Nav: "sessions", Data: data})
		return
	}

	tokens, err := d.dex.listRefresh(r.Context(), sub)
	if err != nil {
		// Searched stays false so the page shows the error alone, rather than an
		// empty result table next to it saying the user has no sessions.
		d.logger.Error("failed to list refresh tokens", "err", err)
		d.render(w, r, "sessions.html", page{Title: "Sessions", Nav: "sessions", Error: friendlyGRPCError(err), Data: data})
		return
	}
	data.Tokens = tokens
	data.Searched = true
	d.render(w, r, "sessions.html", page{Title: "Sessions", Nav: "sessions", Data: data})
}
