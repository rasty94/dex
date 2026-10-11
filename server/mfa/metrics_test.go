package mfa

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/dexidp/dex/server/internal"
	"github.com/dexidp/dex/server/ratelimit"
	"github.com/dexidp/dex/storage"
)

// totpFixture is a handler with one enrolled TOTP authenticator, "totp-1",
// for user-1, and a way to submit codes to it.
type totpFixture struct {
	h       *Handler
	router  http.Handler
	store   storage.Storage
	authReq storage.AuthRequest
	key     *otp.Key
}

func newTOTPFixture(t *testing.T) *totpFixture {
	t.Helper()
	provider := NewTOTPProvider("Test", nil)
	h, router, store := newTestHandler(t, map[string]Provider{"totp-1": provider}, []string{"totp-1"})

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
	return &totpFixture{h: h, router: router, store: store, authReq: authReq, key: key}
}

// newLogin is another login of the same user, as an attacker who has the
// password can start as many as they like.
func (f *totpFixture) newLogin(t *testing.T, id string) storage.AuthRequest {
	t.Helper()
	req := f.authReq
	req.ID, req.HMACKey = id, randomHMACKey(t)
	require.NoError(t, f.store.CreateAuthRequest(t.Context(), req))
	return req
}

func (f *totpFixture) post(authReq storage.AuthRequest, code string) int {
	form := url.Values{
		"req": {authReq.ID}, "authenticator": {"totp-1"}, "totp": {code},
		"hmac": {internal.SignStep(authReq, internal.Authenticator("totp-1"))},
	}
	req := httptest.NewRequest(http.MethodPost, "/mfa/totp", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr.Code
}

func (f *totpFixture) codes(t *testing.T) (good, wrong string) {
	t.Helper()
	good, err := totp.GenerateCode(f.key.Secret(), time.Now())
	require.NoError(t, err)
	wrong = "000000"
	if good == wrong {
		wrong = "111111"
	}
	return good, wrong
}

// Every TOTP check is counted by method and result. (A wrong code also shows up
// as a 401 on /mfa/totp in the HTTP metrics; this one names the method and
// counts replays too.)
func TestTOTPChecksAreCounted(t *testing.T) {
	f := newTOTPFixture(t)
	m, err := NewMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	f.h.Metrics = m
	count := func(result string) float64 {
		return testutil.ToFloat64(m.verifications.WithLabelValues("totp", result))
	}
	good, wrong := f.codes(t)

	f.post(f.authReq, wrong)
	require.Equal(t, 1.0, count("failure"), "a wrong code was not counted")
	require.Equal(t, 0.0, count("success"))

	f.post(f.authReq, good)
	require.Equal(t, 1.0, count("success"), "a right code was not counted")

	// The same code in another login is a replay, refused and counted as a
	// failure. (The same login again is not checked at all: its MFA is done.)
	// validateTOTPCode is what refuses it; the replay branch inside the storage
	// update only catches two concurrent submissions, and is not exercised here.
	f.post(f.newLogin(t, "req-totp-2"), good)
	require.Equal(t, 2.0, count("failure"), "a replayed code was not counted")
	require.Equal(t, 1.0, count("success"))
}

// Six digits are guessable without a budget. Once it is spent, even the right
// code is refused unchecked: that is what makes guessing pointless, and it
// holds across logins, since whoever guesses has the password and can start
// as many as they like.
func TestTOTPAttemptsAreLimited(t *testing.T) {
	f := newTOTPFixture(t)
	f.h.Limiter = ratelimit.New(ratelimit.Config{Enabled: true, Attempts: 3, Window: 15 * time.Minute}, time.Now)
	m, err := NewMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	f.h.Metrics = m
	good, wrong := f.codes(t)

	for i := range 3 {
		require.Equal(t, http.StatusUnauthorized, f.post(f.newLogin(t, "guess-"+string(rune('a'+i))), wrong), "attempt %d is within budget", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, f.post(f.newLogin(t, "guess-d"), good),
		"the right code went through with the budget spent")
	require.Equal(t, 3.0, testutil.ToFloat64(m.verifications.WithLabelValues("totp", "failure")),
		"a refused attempt must not count as a checked one")
	require.Equal(t, 0.0, testutil.ToFloat64(m.verifications.WithLabelValues("totp", "success")))
}

// A right code clears the count, so a user who mistypes now and then is never
// locked out by mistakes spread over time.
func TestTOTPSuccessResetsTheBudget(t *testing.T) {
	f := newTOTPFixture(t)
	f.h.Limiter = ratelimit.New(ratelimit.Config{Enabled: true, Attempts: 3, Window: 15 * time.Minute}, time.Now)
	good, wrong := f.codes(t)

	f.post(f.newLogin(t, "a"), wrong)
	f.post(f.newLogin(t, "b"), wrong)
	require.NotEqual(t, http.StatusTooManyRequests, f.post(f.authReq, good))
	for _, id := range []string{"c", "d", "e"} {
		require.Equal(t, http.StatusUnauthorized, f.post(f.newLogin(t, id), wrong), "the budget was not reset by the right code")
	}
}

// Without a registry the handler must work exactly as before.
func TestNilMetricsCountNothing(t *testing.T) {
	var m *Metrics
	m.verified("totp", false)
}
