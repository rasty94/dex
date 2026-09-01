package authflow

// password.go implements the password-credential login mechanism: the login
// form and the credential check for password connectors.

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/dexidp/dex/connector"
	"github.com/dexidp/dex/connector/keystone"
	"github.com/dexidp/dex/server/ratelimit"
	"github.com/dexidp/dex/server/templates"
	"github.com/dexidp/dex/server/tokens"
	"github.com/dexidp/dex/storage"
)

func (h *Handler) handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	authID := r.URL.Query().Get("state")
	if authID == "" {
		h.renderError(r, w, http.StatusBadRequest, "User session error.")
		return
	}

	backLink := sanitizeBackLink(r.URL.Query().Get("back"))

	authReq, err := h.Storage.GetAuthRequest(ctx, authID)
	if err != nil {
		if err == storage.ErrNotFound {
			h.Logger.ErrorContext(r.Context(), "invalid 'state' parameter provided", "err", err)
			h.renderError(r, w, http.StatusBadRequest, "Requested resource does not exist.")
			return
		}
		h.Logger.ErrorContext(r.Context(), "failed to get auth request", "err", err)
		h.renderError(r, w, http.StatusInternalServerError, "Database error.")
		return
	}

	connID, err := url.PathUnescape(mux.Vars(r)["connector"])
	if err != nil {
		h.Logger.ErrorContext(r.Context(), "failed to parse connector", "err", err)
		h.renderError(r, w, http.StatusBadRequest, "Requested resource does not exist")
		return
	} else if connID != "" && connID != authReq.ConnectorID {
		h.Logger.ErrorContext(r.Context(), "connector mismatch: password login triggered for different connector from authentication start", "start_connector_id", authReq.ConnectorID, "password_connector_id", connID)
		h.renderError(r, w, http.StatusBadRequest, "Requested resource does not exist.")
		return
	}

	conn, err := h.Connectors.Get(ctx, authReq.ConnectorID)
	if err != nil {
		h.Logger.ErrorContext(r.Context(), "failed to get connector", "connector_id", authReq.ConnectorID, "err", err)
		h.renderError(r, w, http.StatusInternalServerError, "Connector failed to initialize.")
		return
	}

	pwConn, ok := conn.Connector.(connector.PasswordConnector)
	if !ok {
		h.Logger.ErrorContext(r.Context(), "expected password connector in handlePasswordLogin()", "password_connector", pwConn)
		h.renderError(r, w, http.StatusInternalServerError, "Requested resource does not exist.")
		return
	}

	rememberMe := h.Sessions.RememberMeDefault()

	// The domain selector only makes sense for a connector that authenticates
	// against more than one, which today means Keystone. Asking storage for the
	// type is what keeps the template free of connector-specific logic.
	form := templates.PasswordForm{}
	if storageConn, serr := h.Storage.GetConnector(ctx, authReq.ConnectorID); serr == nil && storageConn.Type == "keystone" {
		form.ShowDomain = true
	}

	switch r.Method {
	case http.MethodGet:
		// Before rendering the password form, allow connectors that support SPNEGO to try Kerberos auth.
		if sp, ok := pwConn.(connector.SPNEGOAware); ok {
			scopes := tokens.ParseScopes(authReq.Scopes)
			if ident, handled, err := sp.TrySPNEGO(ctx, scopes, w, r); bool(handled) {
				if err != nil {
					// SPNEGO handled the request but reported an error (e.g., LDAP lookup failed
					// after successful Kerberos auth). Log error details, show generic message to user.
					h.Logger.ErrorContext(ctx, "SPNEGO authentication error", "err", err)
					h.renderError(r, w, http.StatusUnauthorized, ErrMsgAuthenticationFailed)
					return
				}
				if ident != nil {
					authReq, err = h.finalizeLogin(ctx, *ident, authReq, conn.Connector)
					if err != nil {
						h.Logger.ErrorContext(ctx, "failed to finalize login", "err", err)
						h.renderError(r, w, http.StatusInternalServerError, "Login error.")
						return
					}
					http.Redirect(w, r, h.buildContinueURL(authReq), http.StatusSeeOther)
					return
				}
				// handled with no identity typically means the SPNEGO middleware
				// wrote its own 401 (bare challenge, continuation, or reject); do
				// not render the password form on top of it.
				return
			}
		}
		if err := h.Templates.Password(r, w, r.URL.String(), "", usernamePrompt(pwConn), false, backLink, rememberMe, form); err != nil {
			h.Logger.ErrorContext(r.Context(), "server template error", "err", err)
		}
	case http.MethodPost:
		username := r.FormValue("login")
		password := r.FormValue("password")
		scopes := tokens.ParseScopes(authReq.Scopes)

		// Keystone reads these off the context: the domain for this attempt, and
		// the code plus receipt when the user is answering the second-factor
		// step. Empty values are left off so the connector keeps its own default.
		form.Domain = r.FormValue("domain")
		if form.ShowDomain && form.Domain != "" {
			r = r.WithContext(context.WithValue(r.Context(), keystone.DomainContextKey, form.Domain))
		}
		if totp := r.FormValue("totp"); totp != "" {
			r = r.WithContext(context.WithValue(r.Context(), keystone.TOTPContextKey, totp))
		}
		if receipt := r.FormValue("receipt"); receipt != "" {
			r = r.WithContext(context.WithValue(r.Context(), keystone.ReceiptContextKey, receipt))
		}

		// Throttle before hitting the upstream provider: a failed attempt is what
		// an attacker repeats, and a successful login clears the counter below.
		limitKey := ratelimit.Key(ctx, username)
		if !h.LoginLimiter.Allow(limitKey) {
			h.Logger.WarnContext(ctx, "login rate limit exceeded", "user", username)
			h.renderError(r, w, http.StatusTooManyRequests, "Too many login attempts. Please try again later.")
			return
		}

		identity, ok, err := pwConn.Login(r.Context(), scopes, username, password)
		if err != nil {
			// Not a failure: the password was accepted and the provider wants a
			// second factor. Re-render the form in its second-factor step,
			// carrying the receipt that ties it to this exchange.
			var errTOTP keystone.ErrTOTPRequired
			if errors.As(err, &errTOTP) {
				form.RequireTOTP = true
				form.Receipt = errTOTP.Receipt
				if err := h.Templates.Password(r, w, r.URL.String(), username, usernamePrompt(pwConn), false, backLink, rememberMe, form); err != nil {
					h.Logger.ErrorContext(r.Context(), "server template error", "err", err)
				}
				return
			}

			h.Logger.ErrorContext(r.Context(), "failed to login user", "err", err)
			h.renderError(r, w, http.StatusInternalServerError, ErrMsgLoginError)
			return
		}
		if !ok {
			// A receipt in the request means the password step was already
			// cleared, so what just failed was the code. Staying on the
			// second-factor step lets the user retry it without re-entering the
			// password; dropping back to the credential form would also throw
			// away the receipt and force the whole exchange again.
			if form.Receipt = r.FormValue("receipt"); form.Receipt != "" {
				form.RequireTOTP = true
			}
			if err := h.Templates.Password(r, w, r.URL.String(), username, usernamePrompt(pwConn), true, backLink, rememberMe, form); err != nil {
				h.Logger.ErrorContext(r.Context(), "server template error", "err", err)
			}
			h.Logger.ErrorContext(r.Context(), "failed login attempt: Invalid credentials.", "user", username)
			return
		}
		h.LoginLimiter.Reset(limitKey)

		authReq, err = h.finalizeLogin(r.Context(), identity, authReq, conn.Connector)
		if err != nil {
			h.Logger.ErrorContext(r.Context(), "failed to finalize login", "err", err)
			h.renderError(r, w, http.StatusInternalServerError, "Login error.")
			return
		}

		rememberMe := r.FormValue("remember_me") == "on"
		if err := h.Sessions.CreateOrUpdateAuthSession(ctx, r, w, authReq, rememberMe); err != nil {
			h.Logger.ErrorContext(ctx, "failed to create/update auth session", "err", err)
		}

		http.Redirect(w, r, h.buildContinueURL(authReq), http.StatusSeeOther)
	default:
		h.renderError(r, w, http.StatusBadRequest, "Unsupported request method.")
	}
}

// sanitizeBackLink permits only a same-origin absolute path as the "Select
// another login method" target. The legitimate value is always a rooted path
// built from the issuer path (see login.go), so anything that could redirect
// off-origin — an absolute URL, a scheme-relative "//host" or "/\host" that
// browsers treat as protocol-relative, or a value that fails to parse — is
// dropped rather than rendered as a link (open-redirect prevention).
func sanitizeBackLink(back string) string {
	if back == "" {
		return ""
	}
	u, err := url.Parse(back)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	if back[0] != '/' {
		return ""
	}
	if len(back) >= 2 && (back[1] == '/' || back[1] == '\\') {
		return ""
	}
	return back
}

// Check for username prompt override from connector. Defaults to "Username".
func usernamePrompt(conn connector.PasswordConnector) string {
	if attr := conn.Prompt(); attr != "" {
		return attr
	}
	return "Username"
}
