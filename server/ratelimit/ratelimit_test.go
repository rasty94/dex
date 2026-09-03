package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dexidp/dex/server/reqctx"
)

func TestLimiter(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	l := New(Config{Enabled: true, Attempts: 3, Window: time.Minute}, func() time.Time { return now })

	// The burst is spent first, then attempts are refused.
	for i := 0; i < 3; i++ {
		require.True(t, l.Allow(ctx, "a"), "attempt %d should be allowed", i)
	}
	require.False(t, l.Allow(ctx, "a"))

	// Other keys have their own budget.
	require.True(t, l.Allow(ctx, "b"))

	// A successful login clears the failed attempts, restoring the full budget.
	l.Reset(ctx, "a")
	for i := 0; i < 3; i++ {
		require.True(t, l.Allow(ctx, "a"), "attempt %d after reset should be allowed", i)
	}
	require.False(t, l.Allow(ctx, "a"))

	// Tokens come back as the window elapses.
	now = now.Add(time.Minute)
	require.True(t, l.Allow(ctx, "a"))
}

func TestLimiterDisabled(t *testing.T) {
	ctx := t.Context()
	l := New(Config{Enabled: false, Attempts: 1, Window: time.Minute}, nil)
	require.Nil(t, l)

	// A nil limiter must not throttle, and must not panic.
	for i := 0; i < 100; i++ {
		require.True(t, l.Allow(ctx, "a"))
	}
	l.Reset(ctx, "a")
	l.SetRejectedCounter(nil)
}

// An unset budget must not divide by zero or refuse everything.
func TestLimiterDefaults(t *testing.T) {
	ctx := t.Context()
	l := New(Config{Enabled: true}, nil)
	require.NotNil(t, l)
	require.Equal(t, defaultAttempts, l.burst)

	for i := 0; i < defaultAttempts; i++ {
		require.True(t, l.Allow(ctx, "a"), "attempt %d should be allowed", i)
	}
	require.False(t, l.Allow(ctx, "a"))
}

func TestLimiterEvictsIdleBuckets(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	l := New(Config{Enabled: true, Attempts: 1, Window: time.Minute}, func() time.Time { return now })

	l.Allow(ctx, "stale")
	now = now.Add(idleTTL + time.Second)
	l.Allow(ctx, "fresh")

	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotContains(t, l.buckets, "stale")
	require.Contains(t, l.buckets, "fresh")
}

func TestKey(t *testing.T) {
	ctx := reqctx.WithRemoteIP(context.Background(), "203.0.113.7")

	// The username is folded so casing cannot buy a fresh bucket, and the IP is
	// part of the key so one address cannot lock out every user.
	require.Equal(t, Key(ctx, "Jane"), Key(ctx, "jane"))
	require.NotEqual(t, Key(ctx, "jane"), Key(ctx, "john"))

	other := reqctx.WithRemoteIP(context.Background(), "198.51.100.4")
	require.NotEqual(t, Key(ctx, "jane"), Key(other, "jane"))

	// A context with no IP still produces a usable key rather than panicking.
	require.NotEmpty(t, Key(context.Background(), "jane"))
}
