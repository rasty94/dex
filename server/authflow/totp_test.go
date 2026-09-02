package authflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dexidp/dex/connector"
	"github.com/dexidp/dex/connector/keystone"
	"github.com/dexidp/dex/server/connectors"
	"github.com/dexidp/dex/server/internal"
	"github.com/dexidp/dex/server/templates"
	"github.com/dexidp/dex/storage"
	dexweb "github.com/dexidp/dex/web"
)

// totpConnector stands in for Keystone: it reports that a second factor is
// required until it is given a code, and records what reached it through the
// context so the tests can check the handler forwarded the form values.
type totpConnector struct {
	// seen records the context values of the last Login call.
	seenDomain  string
	seenTOTP    string
	seenReceipt string

	// acceptCode is the code that completes the login. Any other code fails.
	acceptCode string

	// issuesToken is the provider token Login hands back through the context
	// after a successful second factor; validToken is the one TokenIdentity
	// still accepts, so a test can revoke it upstream by making them differ.
	issuesToken     string
	validToken      string
	seenRevalidated string
}

func (c *totpConnector) Close() error   { return nil }
func (c *totpConnector) Prompt() string { return "" }

func (c *totpConnector) Login(ctx context.Context, _ connector.Scopes, username, password string) (connector.Identity, bool, error) {
	c.seenDomain, _ = ctx.Value(keystone.DomainContextKey).(string)
	c.seenTOTP, _ = ctx.Value(keystone.TOTPContextKey).(string)
	c.seenReceipt, _ = ctx.Value(keystone.ReceiptContextKey).(string)

	if c.seenReceipt == "" {
		if password != "correct-password" {
			return connector.Identity{}, false, nil
		}
		// "nofactor" stands for a user the provider does not challenge, which is
		// the only way a login completes without a code.
		if username != "nofactor" {
			return connector.Identity{}, false, keystone.ErrTOTPRequired{Receipt: "receipt-abc"}
		}
	} else if c.seenTOTP != c.acceptCode {
		// Second factor: the receipt proves the password step already passed.
		return connector.Identity{}, false, nil
	}
	if out, ok := ctx.Value(keystone.IssuedTokenContextKey).(*string); ok && out != nil {
		*out = c.issuesToken
	}
	return connector.Identity{UserID: "user-1", Username: username, Email: "user@example.com"}, true, nil
}

// newTOTPTest wires the flow to a Keystone-typed connector backed by the fake.
func newTOTPTest(t *testing.T) (*testServer, *totpConnector, string) {
	t.Helper()

	httpServer, server := newTestHandler(t, nil)
	t.Cleanup(httpServer.Close)

	ctx := t.Context()
	conn := &totpConnector{acceptCode: "123456"}

	// The stored connector's type is what turns the domain selector on, so it
	// has to be "keystone" and not just any password connector.
	require.NoError(t, server.Storage.CreateConnector(ctx, storage.Connector{
		ID: "keystone", Type: "keystone", Name: "Keystone", ResourceVersion: "1",
	}))
	// The resource version has to match the stored one, or Get treats the cached
	// entry as stale and reopens it through the resolver.
	server.Connectors.Set("keystone", connectors.Connector{Type: "keystone", ResourceVersion: "1", Connector: conn})

	authReq := storage.AuthRequest{
		ID: "totp-req", ClientID: "example-app", ConnectorID: "keystone",
		Expiry: time.Now().Add(time.Hour),
	}
	require.NoError(t, server.Storage.CreateAuthRequest(ctx, authReq))

	return server, conn, authReq.ID
}

func post(t *testing.T, server *testServer, authID string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/keystone/login?state="+authID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

// A connector that answers "password accepted, code required" must not read as a
// failed login: the user has to land on the second-factor step, carrying the
// receipt that ties it to the exchange the provider already started.
func TestPasswordLoginRendersTOTPStep(t *testing.T) {
	server, conn, authID := newTOTPTest(t)

	w := post(t, server, authID, url.Values{
		"login":    {"alice"},
		"password": {"correct-password"},
		"domain":   {"my-domain"},
	})

	require.Equal(t, http.StatusOK, w.Code, "the second-factor step is not a failed login")
	body := w.Body.String()

	require.Contains(t, body, `name="totp"`, "the code field must be rendered")
	require.Contains(t, body, `value="receipt-abc"`, "the receipt must be carried forward")
	require.Contains(t, body, `value="my-domain"`, "the domain must be carried forward")
	require.Contains(t, body, `value="alice"`, "the username must be carried forward")

	// The receipt is what proves the first factor, so re-sending the password
	// would put it in a form field for no reason. Regression guard.
	require.NotContains(t, body, `name="password"`, "the password must not be re-rendered on the TOTP step")

	require.Equal(t, "my-domain", conn.seenDomain, "the domain must reach the connector")
}

// Retrying a wrong code must stay on the second-factor step. Falling back to the
// credential form would throw away the receipt and force the whole exchange again.
func TestPasswordLoginWrongTOTPStaysOnTOTPStep(t *testing.T) {
	server, conn, authID := newTOTPTest(t)

	w := post(t, server, authID, url.Values{
		"login":   {"alice"},
		"receipt": {"receipt-abc"},
		"domain":  {"my-domain"},
		"totp":    {"000000"},
	})

	require.Equal(t, http.StatusUnauthorized, w.Code)
	body := w.Body.String()

	require.Contains(t, body, `name="totp"`, "a wrong code must not drop back to the credential form")
	require.Contains(t, body, `value="receipt-abc"`, "the receipt must survive a wrong code")
	require.NotContains(t, body, `name="password"`)

	require.Equal(t, "000000", conn.seenTOTP, "the code must reach the connector")
	require.Equal(t, "receipt-abc", conn.seenReceipt, "the receipt must reach the connector")
}

// A wrong password has no receipt, so it must go back to the credential form.
func TestPasswordLoginWrongPasswordReturnsToCredentials(t *testing.T) {
	server, _, authID := newTOTPTest(t)

	w := post(t, server, authID, url.Values{
		"login":    {"alice"},
		"password": {"wrong"},
	})

	require.Equal(t, http.StatusUnauthorized, w.Code)
	body := w.Body.String()

	require.Contains(t, body, `name="password"`, "a wrong password must return to the credential form")
	require.NotContains(t, body, `name="totp"`)
}

// The correct code completes the login and hands off to the dispatcher.
func TestPasswordLoginTOTPSuccess(t *testing.T) {
	server, _, authID := newTOTPTest(t)

	w := post(t, server, authID, url.Values{
		"login":   {"alice"},
		"receipt": {"receipt-abc"},
		"totp":    {"123456"},
	})

	require.Equal(t, http.StatusSeeOther, w.Code, "a completed second factor must continue the flow")

	req, err := server.Storage.GetAuthRequest(t.Context(), authID)
	require.NoError(t, err)
	require.True(t, req.LoggedIn, "the auth request must be marked logged in")
}

// The domain selector is driven by the stored connector type, so it shows for
// Keystone and stays out of the way for everything else.
func TestPasswordLoginDomainField(t *testing.T) {
	server, _, authID := newTOTPTest(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/keystone/login?state="+authID, nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `name="domain"`)
	require.Contains(t, w.Body.String(), `name="password"`)
}

// --- trusted devices ---

// trustKey is a 32-byte AES key for the trust cookie in tests.
var trustKey = []byte("0123456789abcdef0123456789abcdef")

// TokenIdentity makes totpConnector a TokenIdentityConnector, which is what
// makes a device trustable: the token can be revalidated later.
func (c *totpConnector) TokenIdentity(ctx context.Context, subjectTokenType, token string) (connector.Identity, error) {
	c.seenRevalidated = token
	if token != c.validToken {
		return connector.Identity{}, errors.New("token rejected by the provider")
	}
	return connector.Identity{UserID: "user-1", Username: "alice", Email: "user@example.com"}, nil
}

// enableTrust turns on trusted devices and tells the fake which token it issues
// on a successful second factor.
func enableTrust(t *testing.T, server *testServer, conn *totpConnector, issued string) {
	t.Helper()
	server.MFATrust = MFATrustConfig{Enabled: true, EncryptionKey: trustKey}
	conn.issuesToken = issued
	conn.validToken = issued
}

// trustCookie returns the trust cookie from a response, or nil.
func trustCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == mfaTrustCookieName("keystone") {
			return c
		}
	}
	return nil
}

// The cookie holds a live provider token, so it must never be readable. This is
// the reason the slice exists: master wrote it in the clear.
func TestTrustCookieIsEncrypted(t *testing.T) {
	server, conn, authID := newTOTPTest(t)
	enableTrust(t, server, conn, "keystone-token-xyz")

	w := post(t, server, authID, url.Values{
		"login": {"alice"}, "receipt": {"receipt-abc"},
		"totp": {"123456"}, "trust_device": {"1"},
	})
	require.Equal(t, http.StatusSeeOther, w.Code)

	c := trustCookie(w)
	require.NotNil(t, c, "a trusted login must set the cookie")
	require.NotContains(t, c.Value, "keystone-token-xyz", "the provider token must not be readable in the cookie")
	require.True(t, c.HttpOnly)

	// It must still open with the right key, or the trust would be useless.
	got, err := internal.DecryptCookieValue(c.Value, trustKey)
	require.NoError(t, err)
	require.Equal(t, "keystone-token-xyz", got)
}

// A login that completes on the password alone must not mint a trust cookie:
// the cookie's whole purpose is to skip the second factor, so issuing one to a
// login that never passed it would turn trust into a bypass. This is why the
// handler requires a code in the request, not merely a successful login.
func TestTrustRequiresPassingTheSecondFactor(t *testing.T) {
	server, conn, authID := newTOTPTest(t)
	enableTrust(t, server, conn, "keystone-token-xyz")

	w := post(t, server, authID, url.Values{
		"login": {"nofactor"}, "password": {"correct-password"}, "trust_device": {"1"},
	})

	require.Equal(t, http.StatusSeeOther, w.Code, "the login itself must succeed")
	require.Nil(t, trustCookie(w), "a login that never passed the second factor must not be trusted")
}

// The second-factor step, on the other hand, is not a completed login, so it
// must not set a cookie either.
func TestTrustNotSetOnTheSecondFactorStep(t *testing.T) {
	server, conn, authID := newTOTPTest(t)
	enableTrust(t, server, conn, "keystone-token-xyz")

	w := post(t, server, authID, url.Values{
		"login": {"alice"}, "password": {"correct-password"}, "trust_device": {"1"},
	})

	require.Equal(t, http.StatusOK, w.Code)
	require.Nil(t, trustCookie(w))
	require.Contains(t, w.Body.String(), "Remember this device", "the checkbox belongs on the second-factor step")
}

// With the cookie present, a GET revalidates the token with the provider and
// skips the form entirely.
func TestTrustedDeviceSkipsLogin(t *testing.T) {
	server, conn, authID := newTOTPTest(t)
	enableTrust(t, server, conn, "keystone-token-xyz")

	req := httptest.NewRequest(http.MethodGet, "/auth/keystone/login?state="+authID, nil)
	req.AddCookie(&http.Cookie{
		Name:  mfaTrustCookieName("keystone"),
		Value: mustSeal(t, "keystone-token-xyz"),
	})
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code, "a trusted device must not see the login form")
	require.Equal(t, "keystone-token-xyz", conn.seenRevalidated, "the token must be revalidated with the provider")

	got, err := server.Storage.GetAuthRequest(t.Context(), authID)
	require.NoError(t, err)
	require.True(t, got.LoggedIn)
}

// Trust lasts only while the provider honors the token. Revoking it upstream
// must end the trust on the next login, not merely at cookie expiry.
func TestTrustEndsWhenProviderRejectsTheToken(t *testing.T) {
	server, conn, authID := newTOTPTest(t)
	enableTrust(t, server, conn, "keystone-token-xyz")
	conn.validToken = "some-other-token" // revoked upstream

	req := httptest.NewRequest(http.MethodGet, "/auth/keystone/login?state="+authID, nil)
	req.AddCookie(&http.Cookie{
		Name:  mfaTrustCookieName("keystone"),
		Value: mustSeal(t, "keystone-token-xyz"),
	})
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `name="password"`, "a rejected token must fall back to the credential form")

	c := trustCookie(w)
	require.NotNil(t, c)
	require.Equal(t, -1, c.MaxAge, "the stale cookie must be cleared")
}

// A cookie sealed with another key (rotated, or forged) must not be honored,
// and must not crash the login either.
func TestTamperedTrustCookieFallsBackToLogin(t *testing.T) {
	server, conn, authID := newTOTPTest(t)
	enableTrust(t, server, conn, "keystone-token-xyz")

	for name, value := range map[string]string{
		"not base64":     "@@@not-base64@@@",
		"wrong key":      mustSealWith(t, "keystone-token-xyz", []byte("ffffffffffffffffffffffffffffffff")),
		"random garbage": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/auth/keystone/login?state="+authID, nil)
			req.AddCookie(&http.Cookie{Name: mfaTrustCookieName("keystone"), Value: value})
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), `name="password"`, "an unopenable cookie must ask for credentials")
			require.Empty(t, conn.seenRevalidated, "a cookie that does not open must never reach the provider")
		})
	}
}

// Trust is scoped per connector: trusting one provider must not trust another.
func TestTrustCookieIsScopedPerConnector(t *testing.T) {
	require.NotEqual(t, mfaTrustCookieName("keystone"), mfaTrustCookieName("keystone-2"))
	// Connector IDs are user-supplied, so anything outside the charset folds to "_".
	require.Equal(t, "dex_mfa_trust_a_b", mfaTrustCookieName("a/b"))
}

// With trust disabled the checkbox must not appear and no cookie may be set,
// even if the form asks for it.
func TestTrustDisabled(t *testing.T) {
	server, conn, authID := newTOTPTest(t)
	conn.issuesToken = "keystone-token-xyz"

	w := post(t, server, authID, url.Values{
		"login": {"alice"}, "receipt": {"receipt-abc"},
		"totp": {"123456"}, "trust_device": {"1"},
	})
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Nil(t, trustCookie(w), "no cookie may be set while trusted devices are disabled")
}

func mustSeal(t *testing.T, token string) string {
	t.Helper()
	return mustSealWith(t, token, trustKey)
}

func mustSealWith(t *testing.T, token string, key []byte) string {
	t.Helper()
	v, err := internal.EncryptCookieValue(token, key)
	require.NoError(t, err)
	return v
}

// --- i18n ---

// The pages must actually come out in the requested language, and the strings
// with a placeholder must interpolate rather than render the raw "%s".
func TestLoginPagesAreTranslated(t *testing.T) {
	tests := []struct {
		name           string
		acceptLanguage string
		wantCredential string // on the password form
		wantSecondStep string // on the second-factor step
	}{
		{name: "default is English", acceptLanguage: "", wantCredential: "Password", wantSecondStep: "Authentication code"},
		{name: "Spanish", acceptLanguage: "es-ES,es;q=0.9", wantCredential: "Contraseña", wantSecondStep: "Código de autenticación"},
		{name: "French", acceptLanguage: "fr", wantCredential: "Mot de passe", wantSecondStep: "Code d&#39;authentification"},
		{name: "German", acceptLanguage: "de-DE", wantCredential: "Passwort", wantSecondStep: "Authentifizierungscode"},
		{name: "Portuguese", acceptLanguage: "pt-PT", wantCredential: "Palavra-passe", wantSecondStep: "Código de autenticação"},
		{name: "unknown falls back to English", acceptLanguage: "kl-KL", wantCredential: "Password", wantSecondStep: "Authentication code"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _, authID := newTOTPTest(t)

			// The credential form.
			req := httptest.NewRequest(http.MethodGet, "/auth/keystone/login?state="+authID, nil)
			req.Header.Set("Accept-Language", tc.acceptLanguage)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), tc.wantCredential)

			// The second-factor step.
			form := url.Values{"login": {"alice"}, "password": {"correct-password"}}
			req = httptest.NewRequest(http.MethodPost, "/auth/keystone/login?state="+authID, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept-Language", tc.acceptLanguage)
			w = httptest.NewRecorder()
			server.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), tc.wantSecondStep)

			// A string with a placeholder must be interpolated, not shown raw.
			require.NotContains(t, w.Body.String(), "%s", "a translation placeholder reached the page unformatted")
		})
	}
}

// The credential form's own text is translated, while the connector's prompt
// stays as the placeholder — naming the prompt inside the error would produce
// "Email Address o contraseña incorrectos", half in each language.
func TestCredentialFormIsFullyTranslated(t *testing.T) {
	server, _, authID := newTOTPTest(t)

	form := url.Values{"login": {"alice"}, "password": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/keystone/login?state="+authID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept-Language", "es")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	body := w.Body.String()

	require.Contains(t, body, "Credenciales incorrectas.")
	require.Contains(t, body, `<label for="login">Usuario</label>`, "the username label is dex's own text and must be translated")
	require.Contains(t, body, `placeholder="username"`, "the connector's prompt stays as the placeholder")
	require.NotContains(t, body, "%s")
	require.NotContains(t, body, "%!")
}

// --- per-client branding ---

// setTheme rebuilds the flow's templates with a per-client theme and a stub for
// the client's own logo, the way the server assembles them from config.
func setTheme(t *testing.T, server *testServer, themes map[string]templates.ClientTheme, clientLogo func(context.Context, string) string) {
	t.Helper()
	//nolint:dogsled // only the templates are needed here
	_, _, _, tmpls, err := templates.LoadWebConfig(templates.Config{
		WebFS:        dexweb.FS(),
		IssuerURL:    server.IssuerURL.String(),
		ClientThemes: themes,
		ClientLogo:   clientLogo,
	})
	require.NoError(t, err)
	server.Templates = tmpls
}

func TestPerClientBranding(t *testing.T) {
	const clientID = "example-app"

	tests := []struct {
		name       string
		themes     map[string]templates.ClientTheme
		clientLogo func(context.Context, string) string
		wantLogo   string
		wantColor  string
	}{
		{
			name:      "no theme leaves the global branding",
			wantLogo:  "theme/logo.png",
			wantColor: "",
		},
		{
			name:      "configured theme wins",
			themes:    map[string]templates.ClientTheme{clientID: {LogoURL: "custom/logo.png", PrimaryColor: "#00aaff"}},
			wantLogo:  "custom/logo.png",
			wantColor: "#00aaff",
		},
		{
			name:       "the client's own logo fills in when the theme sets none",
			themes:     map[string]templates.ClientTheme{clientID: {PrimaryColor: "#123456"}},
			clientLogo: func(context.Context, string) string { return "from-storage.png" },
			wantLogo:   "from-storage.png",
			wantColor:  "#123456",
		},
		{
			name:       "a theme for another client does not leak",
			themes:     map[string]templates.ClientTheme{"someone-else": {LogoURL: "other.png", PrimaryColor: "#abcdef"}},
			clientLogo: func(context.Context, string) string { return "" },
			wantLogo:   "theme/logo.png",
			wantColor:  "",
		},
		{
			name:       "a storage failure falls back to the global logo",
			themes:     map[string]templates.ClientTheme{clientID: {}},
			clientLogo: func(context.Context, string) string { return "" },
			wantLogo:   "theme/logo.png",
			wantColor:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _, authID := newTOTPTest(t)
			setTheme(t, server, tc.themes, tc.clientLogo)

			req := httptest.NewRequest(http.MethodGet, "/auth/keystone/login?state="+authID, nil)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			require.Contains(t, body, tc.wantLogo)

			if tc.wantColor == "" {
				require.NotContains(t, body, "--primary-color", "no color configured, so no style block")
				return
			}
			require.Contains(t, body, "--primary-color: "+tc.wantColor)
		})
	}
}
