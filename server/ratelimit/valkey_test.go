package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

func sharedLimiter(t *testing.T, addr string, attempts int) *Limiter {
	t.Helper()
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Address: addr, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)

	l := New(Config{Enabled: true, Attempts: attempts, Window: time.Minute}, nil)
	l.SetSharedStore(c)
	return l
}

// The whole point of the change: two replicas must share one budget. With local
// buckets each instance would start from zero and the effective limit would be
// attempts x replicas, which is the security hole this closes.
func TestTwoLimitersShareOneBudget(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := sharedLimiter(t, m.Addr(), 3)
	b := sharedLimiter(t, m.Addr(), 3)

	for i := range 3 {
		if !a.Allow(ctx, "1.2.3.4\x00jane") {
			t.Fatalf("attempt %d refused by the first replica", i+1)
		}
	}
	if b.Allow(ctx, "1.2.3.4\x00jane") {
		t.Error("the second replica granted a fourth attempt: the budget is not shared")
	}
}

// A successful login clears the counter, on every replica.
func TestResetClearsTheSharedCounter(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := sharedLimiter(t, m.Addr(), 1)
	b := sharedLimiter(t, m.Addr(), 1)

	a.Allow(ctx, "k")
	if b.Allow(ctx, "k") {
		t.Fatal("the budget was not shared to begin with")
	}
	a.Reset(ctx, "k")
	if !b.Allow(ctx, "k") {
		t.Error("Reset did not clear the shared counter")
	}
}

// The window has to expire, or one burst locks a user out forever.
func TestTheSharedWindowExpires(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	l := sharedLimiter(t, m.Addr(), 1)
	l.Allow(ctx, "k")
	if l.Allow(ctx, "k") {
		t.Fatal("the second attempt should have been refused")
	}

	m.FastForward(time.Minute + time.Second)
	if !l.Allow(ctx, "k") {
		t.Error("the window never expired")
	}
}

// Valkey being down must degrade to today's behavior -- local buckets -- and not
// to "no limit at all".
func TestValkeyDownFallsBackToLocalBuckets(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	l := sharedLimiter(t, m.Addr(), 2)
	m.Close()

	if !l.Allow(ctx, "k") || !l.Allow(ctx, "k") {
		t.Fatal("the local fallback refused attempts inside the budget")
	}
	if l.Allow(ctx, "k") {
		t.Error("with Valkey down the limiter stopped limiting")
	}
}

// dex_login_rate_limit_backend_errors_total exists to say "Valkey is
// unreachable". A client that hangs up mid-login cancels the request context
// and produces an error here too, and counting that would drown the signal in
// normal disconnections.
func TestClientCancellationIsNotCountedAsABackendError(t *testing.T) {
	m := miniredis.RunT(t)
	l := sharedLimiter(t, m.Addr(), 2)

	errs := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_backend_errors_total"})
	l.SetBackendErrorCounter(errs)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	l.Allow(ctx, "k")
	if got := testutil.ToFloat64(errs); got != 0 {
		t.Errorf("a canceled request counted as a backend error: got %v, want 0", got)
	}

	// A store that really is unreachable still has to count.
	m.Close()
	l.Allow(t.Context(), "k")
	if got := testutil.ToFloat64(errs); got != 1 {
		t.Errorf("an unreachable store should count as a backend error: got %v, want 1", got)
	}
}
