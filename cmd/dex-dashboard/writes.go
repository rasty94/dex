package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"

	api "github.com/dexidp/dex/api/v2"
	conns "github.com/dexidp/dex/server/connectors"
	"github.com/dexidp/dex/server/tokens"
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

// handleEndAuthSession ends one signed-in browser. Unlike revoking a refresh
// token, this also revokes the tokens that came from that session, so it is the
// closer thing to "sign this device out".
func (d *dashboard) handleEndAuthSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("session_id"))
	back := backToSessions(r)
	if id == "" {
		http.Error(w, "Missing session.", http.StatusBadRequest)
		return
	}

	sess := sessionFrom(r.Context())
	if err := d.dex.deleteAuthSession(r.Context(), id); err != nil {
		d.logger.Error("failed to end auth session", "err", err, "actor", sess.Email, "session_id", id)
		redirectWith(w, r, back, friendlyGRPCError(err))
		return
	}
	d.logger.Info("auth session ended", "actor", sess.Email, "session_id", id)
	redirectWith(w, r, back, "Ended that session.")
}

// handleEndAllAuthSessions signs a user out of every browser, on every
// connector. Where revoke-all cuts the applications' grants, this cuts the
// sign-ins themselves; an incident usually wants both.
func (d *dashboard) handleEndAllAuthSessions(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.FormValue("user_id"))
	if userID == "" {
		http.Error(w, "Missing user.", http.StatusBadRequest)
		return
	}
	back := backToSessions(r)

	if r.Method == http.MethodGet {
		d.render(w, r, "confirm.html", page{
			Title: "End sessions", Nav: "sessions",
			Data: confirmData{
				Action:  "/sessions/end-all",
				Field:   "user_id",
				Value:   userID,
				Heading: "Sign this user out of every browser?",
				Warning: "Every signed-in browser has to log in again, on every connector. Refresh tokens issued from those sessions are revoked with them; access tokens already issued keep working until they expire.",
				Confirm: "Sign them out everywhere",
				Cancel:  back,
			},
		})
		return
	}

	sess := sessionFrom(r.Context())
	count, err := d.dex.terminateSessionsByUser(r.Context(), userID)
	if err != nil {
		d.logger.Error("failed to terminate sessions", "err", err, "actor", sess.Email, "user_id", userID)
		redirectWith(w, r, back, friendlyGRPCError(err))
		return
	}
	d.logger.Info("sessions terminated", "actor", sess.Email, "user_id", userID, "count", count)
	redirectWith(w, r, back, fmt.Sprintf("Ended %d session(s).", count))
}

// handleDeleteMFASecret removes one enrolled second factor. This is the help
// desk's "I lost my phone" button: without it the only way back for a user whose
// authenticator is gone is erasing the whole identity. Removing the last factor
// leaves the account on its password alone until the user enrolls again, which is
// why it is a fresh-login action rather than a plain row button.
func (d *dashboard) handleDeleteMFASecret(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.FormValue("user_id"))
	connID := strings.TrimSpace(r.FormValue("conn_id"))
	authID := strings.TrimSpace(r.FormValue("authenticator_id"))
	if userID == "" || connID == "" || authID == "" {
		http.Error(w, "Missing user, connector or authenticator.", http.StatusBadRequest)
		return
	}
	back := backToSessions(r)

	sess := sessionFrom(r.Context())
	notFound, err := d.dex.deleteMFASecret(r.Context(), userID, connID, authID)
	if err != nil {
		d.logger.Error("failed to delete MFA secret", "err", err, "actor", sess.Email,
			"user_id", userID, "connector_id", connID, "authenticator", authID)
		redirectWith(w, r, back, friendlyGRPCError(err))
		return
	}
	if notFound {
		redirectWith(w, r, back, "That second factor was already gone.")
		return
	}
	d.logger.Info("MFA secret deleted", "actor", sess.Email,
		"user_id", userID, "connector_id", connID, "authenticator", authID)
	redirectWith(w, r, back, "Removed the second factor "+authID+".")
}

// handlePurgeIdentity performs the GDPR erasure of one identity. It is the only
// action in the dashboard with no undo, so the confirmation counts what will go
// rather than describing it, and names the one consequence the action's title
// does not suggest: dex's password store is keyed by email alone, so purging an
// identity on any connector also deletes the local account using that address.
func (d *dashboard) handlePurgeIdentity(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(firstNonEmpty(r.FormValue("user_id"), r.URL.Query().Get("user_id")))
	connID := strings.TrimSpace(firstNonEmpty(r.FormValue("conn_id"), r.URL.Query().Get("conn_id")))
	if userID == "" || connID == "" {
		http.Error(w, "Missing user or connector.", http.StatusBadRequest)
		return
	}
	back := backToSessions(r)

	if r.Method == http.MethodGet {
		d.render(w, r, "confirm.html", page{
			Title: "Purge identity", Nav: "sessions",
			Data: d.purgeConfirmation(r, userID, connID, back),
		})
		return
	}

	sess := sessionFrom(r.Context())
	notFound, err := d.dex.deleteUserIdentity(r.Context(), userID, connID)
	if err != nil {
		d.logger.Error("failed to purge identity", "err", err, "actor", sess.Email, "user_id", userID, "connector_id", connID)
		redirectWith(w, r, back, friendlyGRPCError(err))
		return
	}
	if notFound {
		redirectWith(w, r, "/sessions", "There was no such identity to purge.")
		return
	}
	d.logger.Info("identity purged", "actor", sess.Email, "user_id", userID, "connector_id", connID)
	// Back to the empty form, not to the lookup: there is nothing left to show.
	redirectWith(w, r, "/sessions", "Purged that identity and everything attached to it.")
}

// purgeConfirmation builds the inventory shown before an erasure. Every lookup
// is best-effort: a count that cannot be fetched is left out rather than
// blocking the page, but the fixed consequences are always listed.
func (d *dashboard) purgeConfirmation(r *http.Request, userID, connID, back string) confirmData {
	ctx := r.Context()
	c := confirmData{
		Action:  "/sessions/purge",
		Fields:  map[string]string{"user_id": userID, "conn_id": connID},
		Heading: "Erase " + userID + " on " + connID + ", permanently?",
		Warning: "This is the GDPR erasure. It deletes the identity dex holds for this user on this connector, and everything attached to it. There is no undo.",
		Confirm: "Erase permanently",
		Cancel:  back,
	}

	var email string
	if identity, err := d.dex.getUserIdentity(ctx, userID, connID); err == nil && identity != nil {
		email = identity.Email
		c.Heading = "Erase " + firstNonEmpty(identity.Email, userID) + " on " + connID + ", permanently?"
		if n := len(identity.Consents); n > 0 {
			c.Inventory = append(c.Inventory, fmt.Sprintf("%d consent(s) granted to clients", n))
		}
		if n := len(identity.MfaDevices); n > 0 {
			c.Inventory = append(c.Inventory, fmt.Sprintf("%d registered second factor(s)", n))
		}
	}
	if sessions, err := d.dex.listAuthSessions(ctx, userID, connID); err == nil && len(sessions) > 0 {
		c.Inventory = append(c.Inventory, fmt.Sprintf("%d signed-in browser(s)", len(sessions)))
	}
	if sub, err := tokens.GenSubject(userID, connID); err == nil {
		if refresh, err := d.dex.listRefresh(ctx, sub); err == nil && len(refresh) > 0 {
			c.Inventory = append(c.Inventory, fmt.Sprintf("%d refresh token(s)", len(refresh)))
		}
	}
	c.Inventory = append(c.Inventory, "the identity record itself, with its claims and groups")

	// The cross-connector one. It is last because it is the surprise.
	if pw, err := d.dex.localPasswordFor(ctx, email); err == nil && pw != nil {
		c.Alert = "This also deletes the local password account " + pw.Email +
			", because dex keys passwords by email alone. That account can sign in on its own and is not part of this connector. " +
			"If it comes from dex's config file rather than storage, dex refuses the whole erasure and nothing is deleted: " +
			"remove the user from the config file first."
	}
	return c
}

// handleSignOutConnector signs out everyone who authenticated through one
// connector. The case for it is retiring an identity provider: after the
// connector is gone its sessions would otherwise keep working until they expire
// on their own, which for a provider you no longer trust is exactly wrong.
func (d *dashboard) handleSignOutConnector(w http.ResponseWriter, r *http.Request) {
	connID := strings.TrimSpace(firstNonEmpty(r.FormValue("id"), r.URL.Query().Get("id")))
	if connID == "" {
		http.Error(w, "Missing connector.", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet {
		d.render(w, r, "confirm.html", page{
			Title: "Sign out connector", Nav: "connectors",
			Data: confirmData{
				Action:  "/connectors/sign-out",
				Field:   "id",
				Value:   connID,
				Heading: "Sign out everyone who authenticated through " + connID + "?",
				Warning: "Every browser signed in through this connector has to log in again, and the refresh tokens issued from those sessions are revoked with them. Access tokens already issued keep working until they expire. The connector itself is not deleted.",
				Confirm: "Sign them all out",
				Cancel:  "/connectors",
			},
		})
		return
	}

	sess := sessionFrom(r.Context())
	count, err := d.dex.terminateSessionsByConnector(r.Context(), connID)
	if err != nil {
		d.logger.Error("failed to terminate connector sessions", "err", err, "actor", sess.Email, "connector_id", connID)
		redirectWith(w, r, "/connectors", friendlyGRPCError(err))
		return
	}
	d.logger.Info("connector sessions terminated", "actor", sess.Email, "connector_id", connID, "count", count)
	redirectWith(w, r, "/connectors", fmt.Sprintf("Ended %d session(s) on %s.", count, connID))
}

// firstNonEmpty returns the first non-empty string, so a handler can accept the
// same field from a query string on GET and a form on POST.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// handleRevokeConsent withdraws a user's approval for one client. It is the
// mildest of the actions on this page: nothing is signed out and no token dies,
// the user simply sees the approval screen again next time.
func (d *dashboard) handleRevokeConsent(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.FormValue("user_id"))
	connID := strings.TrimSpace(r.FormValue("conn_id"))
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	back := backToSessions(r)
	if userID == "" || connID == "" || clientID == "" {
		http.Error(w, "Missing user, connector or client.", http.StatusBadRequest)
		return
	}

	sess := sessionFrom(r.Context())
	notFound, err := d.dex.revokeConsent(r.Context(), userID, connID, clientID)
	if err != nil {
		d.logger.Error("failed to revoke consent", "err", err, "actor", sess.Email, "client_id", clientID)
		redirectWith(w, r, back, friendlyGRPCError(err))
		return
	}
	if notFound {
		redirectWith(w, r, back, "That consent was already gone.")
		return
	}
	d.logger.Info("consent revoked", "actor", sess.Email, "user_id", userID, "client_id", clientID)
	redirectWith(w, r, back, "Withdrew consent for "+clientID+".")
}

// backToSessions rebuilds the lookup the operator was on, so ending a session
// returns to the same result list instead of an empty search form.
func backToSessions(r *http.Request) string {
	v := url.Values{}
	for _, k := range []string{"sub", "user_id", "conn_id"} {
		if got := strings.TrimSpace(r.FormValue(k)); got != "" {
			v.Set(k, got)
		}
	}
	if len(v) == 0 {
		return "/sessions"
	}
	return "/sessions?" + v.Encode()
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
	allowedConnectors := splitLines(r.FormValue("allowed_connectors"))
	ssoSharedWith := splitLines(r.FormValue("sso_shared_with"))
	postLogoutRedirectURIs := splitLines(r.FormValue("post_logout_redirect_uris"))
	backchannelLogoutURI := strings.TrimSpace(r.FormValue("backchannel_logout_uri"))
	refreshTokenLifetime := strings.TrimSpace(r.FormValue("refresh_token_lifetime"))

	if r.FormValue("editing") == "1" {
		// The two single-value fields are sent as pointers even when empty: dex
		// made them optional so that an empty value clears them, which is the
		// only way to relieve a client of a back-channel endpoint. The lists
		// cannot be cleared the same way -- an empty repeated field does not
		// travel -- and the form says so rather than pretending otherwise.
		notFound, err := d.dex.updateClient(r.Context(), &api.UpdateClientReq{
			Id:                     id,
			Name:                   name,
			LogoUrl:                strings.TrimSpace(r.FormValue("logo_url")),
			RedirectUris:           redirectURIs,
			TrustedPeers:           trustedPeers,
			AllowedConnectors:      allowedConnectors,
			SsoSharedWith:          ssoSharedWith,
			PostLogoutRedirectUris: postLogoutRedirectURIs,
			BackchannelLogoutUri:   &backchannelLogoutURI,
			RefreshTokenLifetime:   &refreshTokenLifetime,
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
		Id:                     id,
		Name:                   name,
		LogoUrl:                strings.TrimSpace(r.FormValue("logo_url")),
		RedirectUris:           redirectURIs,
		TrustedPeers:           trustedPeers,
		Public:                 r.FormValue("public") == "on",
		Secret:                 strings.TrimSpace(r.FormValue("secret")),
		AllowedConnectors:      allowedConnectors,
		SsoSharedWith:          ssoSharedWith,
		PostLogoutRedirectUris: postLogoutRedirectURIs,
		BackchannelLogoutUri:   backchannelLogoutURI,
		RefreshTokenLifetime:   refreshTokenLifetime,
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

	// Fields are extra hidden inputs, for actions identified by more than one
	// value. Rendered in key order, which is stable enough for a form.
	Fields map[string]string
	// Inventory lists what the action will actually destroy, so the operator
	// confirms against a count and not against a sentence.
	Inventory []string
	// Alert is a consequence worth its own line, above the button: something the
	// operator would not expect from the action's name.
	Alert string
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
	// GrantTypes are the ones this connector is restricted to; empty means every
	// grant type. AllGrantTypes is the set to offer, taken from dex itself so the
	// form cannot drift from what dex accepts.
	GrantTypes    []string
	AllGrantTypes []string
}

// allGrantTypes is dex's own set of restrictable grant types, sorted so the form
// does not reshuffle itself between renders.
func allGrantTypes() []string {
	out := make([]string, 0, len(conns.ConnectorGrantTypes))
	for gt := range conns.ConnectorGrantTypes {
		out = append(out, gt)
	}
	sort.Strings(out)
	return out
}

// checked reports whether a grant type is in the connector's list. A template
// cannot ask that of a slice on its own.
func (c connectorFormData) Checked(grantType string) bool {
	return slices.Contains(c.GrantTypes, grantType)
}

func (d *dashboard) handleConnectorForm(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	data := connectorFormData{Config: "{}", Types: ConnectorTypes(), AllGrantTypes: allGrantTypes()}

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
		data = connectorFormData{
			ID: conn.Id, Type: conn.Type, Name: conn.Name, Config: shown, Editing: true,
			Types: data.Types, GrantTypes: conn.GrantTypes, AllGrantTypes: data.AllGrantTypes,
		}
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

	// Unlike a client's lists, this one can be emptied: the request wraps the
	// grant types in a message, so "none checked" (unrestricted) is tellable
	// apart from "not mentioned".
	grantTypes := r.Form["grant_types"]

	if editing {
		notFound, err := d.dex.updateConnector(r.Context(), &api.UpdateConnectorReq{
			Id: id, NewType: connType, NewName: name, NewConfig: config,
			NewGrantTypes: &api.GrantTypes{GrantTypes: grantTypes},
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
		Id: id, Type: connType, Name: name, Config: config, GrantTypes: grantTypes,
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
