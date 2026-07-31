package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoginLimiter(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newLoginLimiter(LoginRateLimitConfig{
		Enabled:  true,
		Attempts: 3,
		Window:   time.Minute,
	}, func() time.Time { return now })

	// The burst is spent first, then attempts are refused.
	for i := 0; i < 3; i++ {
		require.True(t, l.allow("a"), "attempt %d should be allowed", i)
	}
	require.False(t, l.allow("a"))

	// Other keys have their own budget.
	require.True(t, l.allow("b"))

	// A successful login clears the failed attempts, restoring the full budget.
	l.reset("a")
	for i := 0; i < 3; i++ {
		require.True(t, l.allow("a"), "attempt %d after reset should be allowed", i)
	}
	require.False(t, l.allow("a"))

	// Tokens come back as the window elapses.
	now = now.Add(time.Minute)
	require.True(t, l.allow("a"))
}

func TestLoginLimiterDisabled(t *testing.T) {
	l := newLoginLimiter(LoginRateLimitConfig{Enabled: false, Attempts: 1, Window: time.Minute}, nil)
	require.Nil(t, l)

	// A nil limiter must not throttle, and must not panic.
	for i := 0; i < 100; i++ {
		require.True(t, l.allow("a"))
	}
	l.reset("a")
}

func TestLoginLimiterEvictsIdleBuckets(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newLoginLimiter(LoginRateLimitConfig{
		Enabled:  true,
		Attempts: 1,
		Window:   time.Minute,
	}, func() time.Time { return now })

	l.allow("stale")
	now = now.Add(loginLimiterIdleTTL + time.Second)
	l.allow("fresh")

	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotContains(t, l.buckets, "stale")
	require.Contains(t, l.buckets, "fresh")
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{"remote addr", "10.0.0.1:1234", "", "10.0.0.1"},
		{"remote addr without port", "10.0.0.1", "", "10.0.0.1"},
		{"single proxy", "10.0.0.1:1234", "203.0.113.5", "203.0.113.5"},
		{"proxy chain uses the client", "10.0.0.1:1234", "203.0.113.5, 10.0.0.2", "203.0.113.5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/token", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			require.Equal(t, tc.want, clientIP(r))
		})
	}
}

// The password grant must stop hammering the upstream connector once a client
// has burned through its failed attempts, and forgive it after a success.
func TestHandlePasswordGrantRateLimit(t *testing.T) {
	httpServer, s := newTestServer(t, func(c *Config) {
		c.PasswordConnector = "test"
		c.Now = time.Now
		c.LoginRateLimit = LoginRateLimitConfig{Enabled: true, Attempts: 2, Window: time.Minute}
	})
	defer httpServer.Close()

	mockConnectorDataTestStorage(t, s.storage)

	makeReq := func(username, password, remoteAddr string) *httptest.ResponseRecorder {
		u, err := url.Parse(s.issuerURL.String())
		require.NoError(t, err)
		u.Path = path.Join(u.Path, "/token")

		v := url.Values{}
		v.Add("scope", "openid email")
		v.Add("grant_type", "password")
		v.Add("username", username)
		v.Add("password", password)

		req, _ := http.NewRequest("POST", u.String(), bytes.NewBufferString(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")
		req.SetBasicAuth("test", "barfoo") // NOSONAR
		req.RemoteAddr = remoteAddr

		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		return rr
	}

	require.Equal(t, 401, makeReq("test", "invalid", "10.0.0.1:1234").Code)
	require.Equal(t, 401, makeReq("test", "invalid", "10.0.0.1:1234").Code)
	require.Equal(t, http.StatusTooManyRequests, makeReq("test", "invalid", "10.0.0.1:1234").Code)

	// Another address is unaffected, and a valid login is not throttled.
	require.Equal(t, 200, makeReq("test", "test", "10.0.0.2:1234").Code)

	// The successful login cleared that key's counter.
	require.Equal(t, 401, makeReq("test", "invalid", "10.0.0.2:1234").Code)
}
