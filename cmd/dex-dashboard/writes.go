package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/crypto/bcrypt"

	api "github.com/dexidp/dex/api/v2"
)

// bcryptCost matches what dex's own docs use for static passwords. Higher costs
// are slower to verify on every login, not just on the one write done here.
const bcryptCost = 10

// splitLines turns a textarea into a list, dropping blanks. Redirect URIs and
// trusted peers are one per line: comma separation is ambiguous in a URL.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// redirectWith sends the browser back to a listing with a one-line result.
// Redirecting rather than rendering means a reload does not repeat the write.
func redirectWith(w http.ResponseWriter, r *http.Request, path, msg string) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	http.Redirect(w, r, path+sep+"msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

// ─────────────────────────── sessions ───────────────────────────

// handleRevokeRefresh cuts one client's refresh token for one user. It is the
// action an operator actually reaches for during an incident, and it is
// reversible: the user signs in again.
func (d *dashboard) handleRevokeRefresh(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimSpace(r.FormValue("sub"))
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	if sub == "" || clientID == "" {
		http.Error(w, "Missing subject or client.", http.StatusBadRequest)
		return
	}

	notFound, err := d.dex.revokeRefresh(r.Context(), sub, clientID)
	if err != nil {
		d.logger.Error("failed to revoke refresh token", "err", err, "client_id", clientID)
		d.renderResult(w, r, "sessions", friendlyGRPCError(err))
		return
	}

	sess := sessionFrom(r.Context())
	if notFound {
		d.logger.Info("refresh token already gone", "actor", sess.Email, "client_id", clientID)
		redirectWith(w, r, "/sessions", "That refresh token was already gone.")
		return
	}
	d.logger.Info("refresh token revoked", "actor", sess.Email, "client_id", clientID)
	redirectWith(w, r, "/sessions", "Revoked the refresh token for "+clientID+".")
}

// handleRevokeAllRefresh cuts every session a user holds. This is the offboarding
// and lost-laptop button: going client by client during an incident is how one
// gets missed.
func (d *dashboard) handleRevokeAllRefresh(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimSpace(r.FormValue("sub"))
	if sub == "" {
		http.Error(w, "Missing subject.", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet {
		count := "every"
		if tokens, err := d.dex.listRefresh(r.Context(), sub); err == nil {
			count = fmt.Sprintf("all %d", len(tokens))
		}
		d.render(w, r, "confirm.html", page{
			Title: "Revoke sessions", Nav: "sessions",
			Data: confirmData{
				Action:  "/sessions/revoke-all",
				Field:   "sub",
				Value:   sub,
				Heading: "Revoke " + count + " of this user's refresh tokens?",
				Warning: "Every application holding one has to send the user through a fresh login. Access tokens already issued keep working until they expire, so this is not instant lockout.",
				Confirm: "Revoke them all",
				Cancel:  "/sessions?sub=" + url.QueryEscape(sub),
			},
		})
		return
	}

	sess := sessionFrom(r.Context())
	revoked, err := d.dex.revokeAllRefresh(r.Context(), sub)
	if err != nil {
		d.logger.Error("failed to revoke all refresh tokens", "err", err, "actor", sess.Email, "revoked", revoked)
		redirectWith(w, r, "/sessions?sub="+url.QueryEscape(sub),
			fmt.Sprintf("Revoked %d before failing: %s", revoked, friendlyGRPCError(err)))
		return
	}
	d.logger.Info("all refresh tokens revoked", "actor", sess.Email, "count", revoked)
	redirectWith(w, r, "/sessions?sub="+url.QueryEscape(sub),
		fmt.Sprintf("Revoked %d refresh token(s).", revoked))
}

// ─────────────────────────── clients ───────────────────────────

func (d *dashboard) handleClientForm(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	data := struct {
		Client  *api.Client
		Editing bool
	}{Client: &api.Client{}}

	if id != "" {
		client, err := d.dex.getClient(r.Context(), id)
		if err != nil {
			d.logger.Error("failed to get client", "err", err, "client_id", id)
			d.render(w, r, "client_form.html", page{Title: "Client", Nav: "clients", Error: friendlyGRPCError(err), Data: data})
			return
		}
		data.Client, data.Editing = client, true
	}

	title := "New client"
	if data.Editing {
		title = "Edit " + data.Client.Id
	}
	d.render(w, r, "client_form.html", page{Title: title, Nav: "clients", Data: data})
}

func (d *dashboard) handleClientSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		d.renderResult(w, r, "clients", "A client needs an ID.")
		return
	}

	redirectURIs := splitLines(r.FormValue("redirect_uris"))
	trustedPeers := splitLines(r.FormValue("trusted_peers"))
	name := strings.TrimSpace(r.FormValue("name"))

	if r.FormValue("editing") == "1" {
		notFound, err := d.dex.updateClient(r.Context(), &api.UpdateClientReq{
			Id:           id,
			Name:         name,
			LogoUrl:      strings.TrimSpace(r.FormValue("logo_url")),
			RedirectUris: redirectURIs,
			TrustedPeers: trustedPeers,
		})
		if err != nil {
			d.logger.Error("failed to update client", "err", err, "client_id", id)
			d.renderResult(w, r, "clients", friendlyGRPCError(err))
			return
		}
		if notFound {
			d.renderResult(w, r, "clients", "No client with ID "+id+".")
			return
		}
		d.logger.Info("client updated", "actor", sess.Email, "client_id", id)
		redirectWith(w, r, "/clients", "Updated "+id+".")
		return
	}

	// The secret is only set at creation: dex's UpdateClient cannot change it,
	// so the form does not pretend otherwise.
	client := &api.Client{
		Id:           id,
		Name:         name,
		LogoUrl:      strings.TrimSpace(r.FormValue("logo_url")),
		RedirectUris: redirectURIs,
		TrustedPeers: trustedPeers,
		Public:       r.FormValue("public") == "on",
		Secret:       strings.TrimSpace(r.FormValue("secret")),
	}
	if !client.Public && client.Secret == "" {
		d.renderResult(w, r, "clients", "A confidential client needs a secret.")
		return
	}

	exists, err := d.dex.createClient(r.Context(), client)
	if err != nil {
		d.logger.Error("failed to create client", "err", err, "client_id", id)
		d.renderResult(w, r, "clients", friendlyGRPCError(err))
		return
	}
	if exists {
		d.renderResult(w, r, "clients", "A client with ID "+id+" already exists.")
		return
	}
	d.logger.Info("client created", "actor", sess.Email, "client_id", id)
	redirectWith(w, r, "/clients", "Created "+id+".")
}

// handleClientDelete shows a confirmation page on GET and deletes on POST.
// Deleting a client breaks every login for that application, so it gets a page
// that says so rather than a dialog that gets clicked through.
func (d *dashboard) handleClientDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Error(w, "Missing client id.", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet {
		d.render(w, r, "confirm.html", page{
			Title: "Delete client", Nav: "clients",
			Data: confirmData{
				Action:  "/clients/delete",
				Field:   "id",
				Value:   id,
				Heading: "Delete client " + id + "?",
				Warning: "Every application signing in through this client stops working immediately. Existing sessions keep their tokens until those expire.",
				Confirm: "Delete client",
				Cancel:  "/clients",
			},
		})
		return
	}

	notFound, err := d.dex.deleteClient(r.Context(), id)
	if err != nil {
		d.logger.Error("failed to delete client", "err", err, "client_id", id)
		d.renderResult(w, r, "clients", friendlyGRPCError(err))
		return
	}
	sess := sessionFrom(r.Context())
	if notFound {
		redirectWith(w, r, "/clients", "No client with ID "+id+".")
		return
	}
	d.logger.Info("client deleted", "actor", sess.Email, "client_id", id)
	redirectWith(w, r, "/clients", "Deleted "+id+".")
}

// ─────────────────────────── local users ───────────────────────────

func (d *dashboard) handleUserForm(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	data := struct {
		Email    string
		Username string
		Editing  bool
	}{Email: email, Username: r.URL.Query().Get("username"), Editing: email != ""}

	title := "New local user"
	if data.Editing {
		title = "Change password for " + email
	}
	d.render(w, r, "user_form.html", page{Title: title, Nav: "users", Data: data})
}

func (d *dashboard) handleUserSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	username := strings.TrimSpace(r.FormValue("username"))

	if email == "" {
		d.renderResult(w, r, "users", "A user needs an email address.")
		return
	}
	if password == "" {
		d.renderResult(w, r, "users", "A password is required.")
		return
	}
	if password != r.FormValue("password_confirm") {
		d.renderResult(w, r, "users", "The two passwords do not match.")
		return
	}

	// Hashed here, so the plaintext never reaches dex's API or its logs.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		d.logger.Error("failed to hash password", "err", err)
		d.renderResult(w, r, "users", "Could not hash the password.")
		return
	}

	if r.FormValue("editing") == "1" {
		notFound, err := d.dex.updatePassword(r.Context(), &api.UpdatePasswordReq{
			Email:       email,
			NewHash:     hash,
			NewUsername: username,
		})
		if err != nil {
			d.logger.Error("failed to update password", "err", err)
			d.renderResult(w, r, "users", friendlyGRPCError(err))
			return
		}
		if notFound {
			d.renderResult(w, r, "users", "No local user with email "+email+".")
			return
		}
		d.logger.Info("local user password changed", "actor", sess.Email, "user", email)
		redirectWith(w, r, "/users", "Changed the password for "+email+".")
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	if userID == "" {
		userID = email
	}
	exists, err := d.dex.createPassword(r.Context(), &api.Password{
		Email:    email,
		Hash:     hash,
		Username: username,
		UserId:   userID,
	})
	if err != nil {
		d.logger.Error("failed to create password", "err", err)
		d.renderResult(w, r, "users", friendlyGRPCError(err))
		return
	}
	if exists {
		d.renderResult(w, r, "users", "A local user with email "+email+" already exists.")
		return
	}
	d.logger.Info("local user created", "actor", sess.Email, "user", email)
	redirectWith(w, r, "/users", "Created "+email+".")
}

func (d *dashboard) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	if email == "" {
		http.Error(w, "Missing email.", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet {
		d.render(w, r, "confirm.html", page{
			Title: "Delete user", Nav: "users",
			Data: confirmData{
				Action:  "/users/delete",
				Field:   "email",
				Value:   email,
				Heading: "Delete local user " + email + "?",
				Warning: "They can no longer sign in with a password. Any session they already hold keeps working until its tokens expire; revoke those from the Sessions view.",
				Confirm: "Delete user",
				Cancel:  "/users",
			},
		})
		return
	}

	notFound, err := d.dex.deletePassword(r.Context(), email)
	if err != nil {
		d.logger.Error("failed to delete password", "err", err)
		d.renderResult(w, r, "users", friendlyGRPCError(err))
		return
	}
	sess := sessionFrom(r.Context())
	if notFound {
		redirectWith(w, r, "/users", "No local user with email "+email+".")
		return
	}
	d.logger.Info("local user deleted", "actor", sess.Email, "user", email)
	redirectWith(w, r, "/users", "Deleted "+email+".")
}

// confirmData drives the shared confirmation page.
type confirmData struct {
	Action  string
	Field   string
	Value   string
	Heading string
	Warning string
	Confirm string
	Cancel  string
}

// renderResult re-renders a listing with an error banner, for failures that
// happen after the form was submitted.
func (d *dashboard) renderResult(w http.ResponseWriter, r *http.Request, nav, msg string) {
	switch nav {
	case "clients":
		d.renderList(w, r, "clients.html", "Clients", "clients", func() (any, error) {
			return d.dex.listClients(r.Context())
		}, msg)
	case "users":
		d.renderList(w, r, "users.html", "Local users", "users", func() (any, error) {
			return d.dex.listPasswords(r.Context())
		}, msg)
	case "connectors":
		d.renderList(w, r, "connectors.html", "Connectors", "connectors", func() (any, error) {
			return d.dex.listConnectors(r.Context())
		}, msg)
	default:
		http.Error(w, msg, http.StatusBadRequest)
	}
}

// ─────────────────────────── connectors ───────────────────────────

type connectorFormData struct {
	ID      string
	Type    string
	Name    string
	Config  string
	Editing bool
	Types   []string
}

func (d *dashboard) handleConnectorForm(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	data := connectorFormData{Config: "{}", Types: ConnectorTypes()}

	// Creating a connector with a type chosen: start from that type's real
	// shape instead of an empty object. The form reloads through here when the
	// operator picks a type, which is why this needs no JavaScript.
	if id == "" {
		if t := strings.TrimSpace(r.URL.Query().Get("type")); t != "" {
			data.Type = t
			if skeleton, err := ConnectorSkeleton(t); err == nil {
				data.Config = skeleton
			} else {
				d.render(w, r, "connector_form.html", page{Title: "New connector", Nav: "connectors", Error: err.Error(), Data: data})
				return
			}
		}
	}

	if id != "" {
		conn, err := d.dex.connector(r.Context(), id)
		if err != nil {
			d.logger.Error("failed to get connector", "err", err, "connector_id", id)
			d.render(w, r, "connector_form.html", page{Title: "Connector", Nav: "connectors", Error: friendlyGRPCError(err), Data: data})
			return
		}
		if conn == nil {
			d.renderResult(w, r, "connectors", "No connector with ID "+id+".")
			return
		}
		shown, err := redactSecrets(conn.Config)
		if err != nil {
			d.logger.Error("failed to render connector config", "err", err, "connector_id", id)
			shown = string(conn.Config)
		}
		data = connectorFormData{ID: conn.Id, Type: conn.Type, Name: conn.Name, Config: shown, Editing: true, Types: data.Types}
	}

	title := "New connector"
	if data.Editing {
		title = "Edit " + data.ID
	}
	d.render(w, r, "connector_form.html", page{Title: title, Nav: "connectors", Data: data})
}

func (d *dashboard) handleConnectorSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id := strings.TrimSpace(r.FormValue("id"))
	connType := strings.TrimSpace(r.FormValue("type"))
	name := strings.TrimSpace(r.FormValue("name"))
	submitted := []byte(strings.TrimSpace(r.FormValue("config")))
	editing := r.FormValue("editing") == "1"

	if id == "" || connType == "" || name == "" {
		d.renderResult(w, r, "connectors", "A connector needs an ID, a type and a name.")
		return
	}

	// Put back any secret the operator left as the marker, using what is
	// already stored, so editing an unrelated field does not wipe a password.
	var stored []byte
	if editing {
		conn, err := d.dex.connector(r.Context(), id)
		if err != nil {
			d.renderResult(w, r, "connectors", friendlyGRPCError(err))
			return
		}
		if conn == nil {
			d.renderResult(w, r, "connectors", "No connector with ID "+id+".")
			return
		}
		stored = conn.Config
	}

	config, err := restoreSecrets(submitted, stored)
	if err != nil {
		d.renderResult(w, r, "connectors", err.Error())
		return
	}

	// dex only checks that the JSON parses. A config that parses but is wrong
	// is accepted and then breaks every login through this connector, so the
	// dashboard checks it against the real type first.
	if err := validateConnectorConfig(connType, config); err != nil {
		d.renderResult(w, r, "connectors", err.Error())
		return
	}

	if editing {
		notFound, err := d.dex.updateConnector(r.Context(), &api.UpdateConnectorReq{
			Id: id, NewType: connType, NewName: name, NewConfig: config,
		})
		if err != nil {
			d.logger.Error("failed to update connector", "err", err, "connector_id", id)
			d.renderResult(w, r, "connectors", friendlyGRPCError(err))
			return
		}
		if notFound {
			d.renderResult(w, r, "connectors", "No connector with ID "+id+".")
			return
		}
		d.logger.Info("connector updated", "actor", sess.Email, "connector_id", id)
		redirectWith(w, r, "/connectors", "Updated "+id+".")
		return
	}

	exists, err := d.dex.createConnector(r.Context(), &api.Connector{
		Id: id, Type: connType, Name: name, Config: config,
	})
	if err != nil {
		d.logger.Error("failed to create connector", "err", err, "connector_id", id)
		d.renderResult(w, r, "connectors", friendlyGRPCError(err))
		return
	}
	if exists {
		d.renderResult(w, r, "connectors", "A connector with ID "+id+" already exists.")
		return
	}
	d.logger.Info("connector created", "actor", sess.Email, "connector_id", id)
	redirectWith(w, r, "/connectors", "Created "+id+".")
}

func (d *dashboard) handleConnectorDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Error(w, "Missing connector id.", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet {
		d.render(w, r, "confirm.html", page{
			Title: "Delete connector", Nav: "connectors",
			Data: confirmData{
				Action:  "/connectors/delete",
				Field:   "id",
				Value:   id,
				Heading: "Delete connector " + id + "?",
				Warning: "Nobody can sign in through this connector any more, and the option disappears from the login screen. Users who signed in through it keep their sessions until those expire.",
				Confirm: "Delete connector",
				Cancel:  "/connectors",
			},
		})
		return
	}

	notFound, err := d.dex.deleteConnector(r.Context(), id)
	if err != nil {
		d.logger.Error("failed to delete connector", "err", err, "connector_id", id)
		d.renderResult(w, r, "connectors", friendlyGRPCError(err))
		return
	}
	sess := sessionFrom(r.Context())
	if notFound {
		redirectWith(w, r, "/connectors", "No connector with ID "+id+".")
		return
	}
	d.logger.Info("connector deleted", "actor", sess.Email, "connector_id", id)
	redirectWith(w, r, "/connectors", "Deleted "+id+".")
}

// handleReloadConfig asks dex to re-read its configuration file. Useful right
// after editing something that came from the file rather than from storage.
func (d *dashboard) handleReloadConfig(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	msg, err := d.dex.reloadConfig(r.Context())
	if err != nil {
		d.logger.Error("failed to reload dex configuration", "err", err)
		d.renderResult(w, r, "connectors", friendlyGRPCError(err))
		return
	}
	d.logger.Info("dex configuration reloaded", "actor", sess.Email)
	redirectWith(w, r, "/connectors", msg)
}
