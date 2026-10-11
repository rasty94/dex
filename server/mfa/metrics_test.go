package mfa

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/dexidp/dex/server/internal"
	"github.com/dexidp/dex/storage"
)

// A wrong TOTP code re-renders the form with 200, so the HTTP metrics cannot
// tell it from a page view: this counter is the only place a guessing attempt
// shows up. The handler has no rate limit of its own.
func TestTOTPChecksAreCounted(t *testing.T) {
	provider := NewTOTPProvider("Test", nil)
	h, router, store := newTestHandler(t, map[string]Provider{"totp-1": provider}, []string{"totp-1"})
	m, err := NewMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	h.Metrics = m

	ctx := t.Context()
	authReq := storage.AuthRequest{
		ID: "req-totp", ClientID: "example-app", Expiry: time.Now().Add(time.Hour),
		HMACKey: randomHMACKey(t), LoggedIn: true, ConnectorID: "mock",
		Claims: storage.Claims{UserID: "user-1", Email: "user@example.com"},
	}
	require.NoError(t, store.CreateAuthRequest(ctx, authReq))
	require.NoError(t, store.CreateClient(ctx, storage.Client{ID: "example-app", Secret: "secret"}))
	key, err := provider.generate("mock", "user@example.com")
	require.NoError(t, err)
	require.NoError(t, store.CreateUserIdentity(ctx, storage.UserIdentity{
		UserID: "user-1", ConnectorID: "mock",
		MFASecrets: map[string]*storage.MFASecret{"totp-1": {AuthenticatorID: "totp-1", Type: "TOTP", Secret: key.String()}},
	}))

	post := func(authReq storage.AuthRequest, code string) {
		form := url.Values{
			"req": {authReq.ID}, "authenticator": {"totp-1"}, "totp": {code},
			"hmac": {internal.SignStep(authReq, internal.Authenticator("totp-1"))},
		}
		req := httptest.NewRequest(http.MethodPost, "/mfa/totp", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	count := func(result string) float64 {
		return testutil.ToFloat64(m.verifications.WithLabelValues("totp", result))
	}

	good, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)
	wrong := "000000"
	if good == wrong {
		wrong = "111111"
	}

	post(authReq, wrong)
	require.Equal(t, 1.0, count("failure"), "a wrong code was not counted")
	require.Equal(t, 0.0, count("success"))

	post(authReq, good)
	require.Equal(t, 1.0, count("success"), "a right code was not counted")

	// The same code in another login is a replay, refused and counted as a
	// failure. (The same login again is not checked at all: its MFA is done.)
	// validateTOTPCode is what refuses it; the replay branch inside the storage
	// update only catches two concurrent submissions, and is not exercised here.
	replay := authReq
	replay.ID, replay.HMACKey = "req-totp-2", randomHMACKey(t)
	require.NoError(t, store.CreateAuthRequest(ctx, replay))
	post(replay, good)
	require.Equal(t, 2.0, count("failure"), "a replayed code was not counted")
	require.Equal(t, 1.0, count("success"))
}

// Without a registry the handler must work exactly as before.
func TestNilMetricsCountNothing(t *testing.T) {
	var m *Metrics
	m.verified("totp", false)
}
