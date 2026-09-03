package keystone

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/dexidp/dex/connector"
)

func TestLoginResult(t *testing.T) {
	tests := []struct {
		name          string
		validPassword bool
		err           error
		want          string
	}{
		{"success", true, nil, "success"},
		{"wrong password", false, nil, "invalid_credentials"},
		{"second factor pending", false, ErrTOTPRequired{Receipt: "r"}, "totp_required"},
		{"upstream error", false, errors.New("boom"), "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := loginResult(tc.validPassword, tc.err); got != tc.want {
				t.Errorf("loginResult() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoginMetricsCountEachStep(t *testing.T) {
	loginAttempts.Reset()

	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	// Without a receipt the first request is answered with one, which is the
	// password step succeeding and the second factor still pending.
	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Openstack-Auth-Receipt") == "" && r.Header.Get("X-Test-Totp") == "" {
			w.Header().Set("openstack-auth-receipt", "receipt-1")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeToken(w, "native-id", "jdoe", "tok-abc")
	})
	mux.HandleFunc("/v3/users/native-id", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "native-id")
	})

	if _, _, err := c.Login(context.Background(), connector.Scopes{}, "jdoe", "pass"); err == nil {
		t.Fatal("expected ErrTOTPRequired on the password step")
	}
	if got := testutil.ToFloat64(loginAttempts.WithLabelValues("password", "totp_required")); got != 1 {
		t.Errorf("password/totp_required = %v, want 1", got)
	}

	// Replaying with the receipt is the second factor step.
	ctx := context.WithValue(context.Background(), ReceiptContextKey, "receipt-1")
	ctx = context.WithValue(ctx, TOTPContextKey, "123456")
	if _, valid, err := c.Login(ctx, connector.Scopes{}, "jdoe", "pass"); err != nil || !valid {
		t.Fatalf("second factor login failed: valid=%v err=%v", valid, err)
	}
	if got := testutil.ToFloat64(loginAttempts.WithLabelValues("totp", "success")); got != 1 {
		t.Errorf("totp/success = %v, want 1", got)
	}
}

func TestTokenIdentityMetricsSkipCacheHits(t *testing.T) {
	tokenValidations.Reset()
	tokenCacheLookups.Reset()

	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)
	c.tokenCache = newTimeCache(time.Hour)

	mux.HandleFunc("/v3/auth/tokens", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "native-id", "jdoe", "tok-abc")
	})
	mux.HandleFunc("/v3/users/native-id", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "native-id")
	})
	mux.HandleFunc("/v3/users/native-id/groups", func(w http.ResponseWriter, r *http.Request) {
		writeGroups(w)
	})

	if _, err := c.TokenIdentity(context.Background(), "", "tok-abc"); err != nil {
		t.Fatalf("TokenIdentity failed: %v", err)
	}
	// Second call is served from the cache and must not be timed as a call to
	// Keystone, otherwise the latency histogram is diluted by cache hits.
	if _, err := c.TokenIdentity(context.Background(), "", "tok-abc"); err != nil {
		t.Fatalf("cached TokenIdentity failed: %v", err)
	}

	if got := testutil.ToFloat64(tokenValidations.WithLabelValues("success")); got != 1 {
		t.Errorf("token validations = %v, want 1", got)
	}
	if got := testutil.ToFloat64(tokenCacheLookups.WithLabelValues("miss")); got != 1 {
		t.Errorf("cache misses = %v, want 1", got)
	}
	if got := testutil.ToFloat64(tokenCacheLookups.WithLabelValues("hit")); got != 1 {
		t.Errorf("cache hits = %v, want 1", got)
	}
}

func TestRefreshMetrics(t *testing.T) {
	refreshAttempts.Reset()

	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "admin-id", "admin", "admin-tok")
	})
	mux.HandleFunc("/v3/users/native-id", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "native-id")
	})

	if _, err := c.Refresh(context.Background(), connector.Scopes{}, connector.Identity{UserID: "native-id"}); err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if got := testutil.ToFloat64(refreshAttempts.WithLabelValues("success")); got != 1 {
		t.Errorf("refresh success = %v, want 1", got)
	}

	// A user that no longer exists must be counted as an error.
	if _, err := c.Refresh(context.Background(), connector.Scopes{}, connector.Identity{UserID: "gone"}); err == nil {
		t.Fatal("expected an error refreshing a deleted user")
	}
	if got := testutil.ToFloat64(refreshAttempts.WithLabelValues("error")); got != 1 {
		t.Errorf("refresh error = %v, want 1", got)
	}
}

// unreachableCache stands in for a shared cache whose Valkey is down.
type unreachableCache struct{}

func (unreachableCache) get(context.Context, string) (connector.Identity, bool, error) {
	return connector.Identity{}, false, errors.New("valkey is unreachable")
}

func (unreachableCache) set(context.Context, string, connector.Identity) {}

// A cache that cannot be reached is not a cache miss. Counted as one, an outage
// looks like a burst of first-time tokens, while in fact every login is paying a
// full round trip to Keystone that the cache was there to save.
func TestAnUnreachableCacheIsNotCountedAsAMiss(t *testing.T) {
	tokenCacheLookups.Reset()

	srv, _ := mockKeystoneServer(t)
	c := newTestConn(srv.URL)
	c.tokenCache = unreachableCache{}

	_, _ = c.TokenIdentity(t.Context(), "urn:ietf:params:oauth:token-type:access_token", "tok")

	if got := testutil.ToFloat64(tokenCacheLookups.WithLabelValues("error")); got != 1 {
		t.Errorf("lookups against an unreachable cache: error = %v, want 1", got)
	}
	if got := testutil.ToFloat64(tokenCacheLookups.WithLabelValues("miss")); got != 0 {
		t.Errorf("an unreachable cache was counted as a miss: miss = %v, want 0", got)
	}
}
