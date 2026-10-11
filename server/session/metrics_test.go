package session

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/dexidp/dex/storage"
	"github.com/dexidp/dex/storage/memory"
)

// Drives a session through the manager the way a browser would: created at
// login, then presented after it ran out.
func TestSessionMetrics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		advance time.Duration
		label   string
	}{
		{"idle", 2 * time.Hour, "idle"},
		{"absolute", 48 * time.Hour, "absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			reg := prometheus.NewRegistry()
			metrics, err := NewMetrics(reg)
			require.NoError(t, err)
			m := &Manager{
				Storage: memory.New(slog.New(slog.DiscardHandler)),
				Config: &Config{
					CookieName: "dex_session", CookieEncryptionKey: make([]byte, 32),
					AbsoluteLifetime: 24 * time.Hour, ValidIfNotUsedFor: time.Hour,
				},
				Now:     func() time.Time { return now },
				Logger:  slog.New(slog.DiscardHandler),
				Metrics: metrics,
			}

			rr := httptest.NewRecorder()
			authReq := storage.AuthRequest{ClientID: "app", ConnectorID: "local", Claims: storage.Claims{UserID: "u1"}}
			require.NoError(t, m.CreateOrUpdateAuthSession(t.Context(), httptest.NewRequest(http.MethodGet, "/", nil), rr, authReq, false))
			require.Equal(t, 1.0, testutil.ToFloat64(metrics.created))

			now = now.Add(tc.advance)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, c := range rr.Result().Cookies() {
				req.AddCookie(c)
			}
			require.Nil(t, m.ValidSession(t.Context(), httptest.NewRecorder(), req), "the session should be over")
			require.Equal(t, 1.0, testutil.ToFloat64(metrics.expired.WithLabelValues(tc.label)),
				"expired as %s was not counted under %q", tc.name, tc.label)
		})
	}
}

func TestCollectedSessions(t *testing.T) {
	metrics, err := NewMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	metrics.Collected(3)
	metrics.Collected(0)
	require.Equal(t, 3.0, testutil.ToFloat64(metrics.expired.WithLabelValues("collected")))

	var none *Metrics
	none.Collected(5)
	none.sessionCreated()
	none.sessionExpired("idle timeout")
}
