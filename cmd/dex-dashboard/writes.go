package main

import (
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
	http.Redirect(w, r, path+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
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
	default:
		http.Error(w, msg, http.StatusBadRequest)
	}
}
