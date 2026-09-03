package ratelimit

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

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
