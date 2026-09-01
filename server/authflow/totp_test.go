package authflow

import (
	"context"
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
	"github.com/dexidp/dex/storage"
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
}

func (c *totpConnector) Close() error   { return nil }
func (c *totpConnector) Prompt() string { return "" }

func (c *totpConnector) Login(ctx context.Context, _ connector.Scopes, username, password string) (connector.Identity, bool, error) {
	c.seenDomain, _ = ctx.Value(keystone.DomainContextKey).(string)
	c.seenTOTP, _ = ctx.Value(keystone.TOTPContextKey).(string)
	c.seenReceipt, _ = ctx.Value(keystone.ReceiptContextKey).(string)

	if c.seenReceipt == "" {
		// First factor. Accept the password, then ask for the code.
		if password != "correct-password" {
			return connector.Identity{}, false, nil
		}
		return connector.Identity{}, false, keystone.ErrTOTPRequired{Receipt: "receipt-abc"}
	}

	// Second factor: the receipt proves the password step already passed.
	if c.seenTOTP != c.acceptCode {
		return connector.Identity{}, false, nil
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
