// Package ratelimit throttles password login attempts before they reach the
// upstream identity provider. It lives in its own package so both the
// interactive auth flow and the password grant can share one limiter without
// an import cycle.
package ratelimit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
	"github.com/dexidp/dex/server/reqctx"
)

// idleTTL bounds how long an unused bucket is kept, so a burst of attempts from
// many addresses does not leak memory.
const idleTTL = 30 * time.Minute

// Config configures the brute force protection applied to the password login
// form and the password grant.
type Config struct {
	Enabled  bool
	Attempts int
	Window   time.Duration
}

// Limiter throttles login attempts keyed by client IP and username. Only failed
// attempts count: a successful login clears the counter for that key, so a
// legitimate user, or a service using the password grant, is never throttled.
//
// With SetSharedStore the counting happens in Valkey, so several replicas share
// one budget. Without it the buckets are in process and the effective limit is
// Attempts x replicas.
type Limiter struct {
	limit rate.Limit
	burst int
	now   func() time.Time

	// Counts refused attempts. Without it there is no way to tell a limit that
	// is stopping an attack from one that is locking real users out.
	rejected prometheus.Counter

	// shared counts in Valkey instead of the local buckets. When it is nil, or
	// when it fails, the buckets below are used: that degrades to the behavior
	// of a single replica rather than to no limit at all.
	shared *dexvalkey.FixedWindow
	window time.Duration

	// Counts falls back to the local buckets. Without it a Valkey that started
	// refusing connections looks exactly like one that is working: the limiter
	// keeps limiting, just per replica again.
	backendErrors prometheus.Counter

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// Default budget applied when the config leaves it unset.
const (
	defaultAttempts = 10
	defaultWindow   = time.Minute
)

// New allows cfg.Attempts failures per cfg.Window, per IP and username. It
// returns nil when rate limiting is disabled; a nil Limiter allows everything.
// Defaults are applied here rather than by the caller, so no consumer can wire
// up a limiter that divides by zero.
func New(cfg Config, now func() time.Time) *Limiter {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Attempts <= 0 {
		cfg.Attempts = defaultAttempts
	}
	if cfg.Window <= 0 {
		cfg.Window = defaultWindow
	}
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		limit:   rate.Every(cfg.Window / time.Duration(cfg.Attempts)),
		burst:   cfg.Attempts,
		now:     now,
		window:  cfg.Window,
		buckets: make(map[string]*bucket),
	}
}

// SetRejectedCounter attaches the metric counting refused attempts.
func (l *Limiter) SetRejectedCounter(c prometheus.Counter) {
	if l == nil {
		return
	}
	l.rejected = c
}

// SetSharedStore makes the limiter count in Valkey, so replicas share a budget.
func (l *Limiter) SetSharedStore(c *dexvalkey.Client) {
	if l == nil || c == nil {
		return
	}
	l.shared = dexvalkey.NewFixedWindow(c, "rl")
}

// SetBackendErrorCounter counts the times the shared store could not be reached
// and the local buckets took over.
func (l *Limiter) SetBackendErrorCounter(c prometheus.Counter) {
	if l == nil {
		return
	}
	l.backendErrors = c
}

// Allow reports whether another login attempt may be made for key.
func (l *Limiter) Allow(ctx context.Context, key string) bool {
	if l == nil {
		return true
	}

	if l.shared != nil {
		n, err := l.shared.Incr(ctx, key, l.window)
		if err == nil {
			if n <= int64(l.burst) {
				return true
			}
			if l.rejected != nil {
				l.rejected.Inc()
			}
			return false
		}
		// Fall through to the local buckets: a Valkey outage must not turn the
		// limiter off. It is counted, because otherwise a store that stopped
		// answering is indistinguishable from one that works.
		// A client that hangs up cancels the request context and lands here
		// too. That is not the store failing, and counting it would leave this
		// metric meaning "Valkey is unreachable, or somebody closed a tab" --
		// which is no alarm at all. A deadline that ran out still counts: the
		// store was too slow to answer inside the budget it was given.
		if l.backendErrors != nil && !errors.Is(err, context.Canceled) {
			l.backendErrors.Inc()
		}
	}

	return l.allowLocal(key)
}

// allowLocal is the in-process token bucket, used when there is no shared store
// and when the shared store cannot be reached.
func (l *Limiter) allowLocal(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.sweep(now)
		b = &bucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now

	if b.limiter.AllowN(now, 1) {
		return true
	}
	if l.rejected != nil {
		l.rejected.Inc()
	}
	return false
}

// Reset forgets the failed attempts recorded for key.
func (l *Limiter) Reset(ctx context.Context, key string) {
	if l == nil {
		return
	}
	if l.shared != nil {
		_ = l.shared.Reset(ctx, key)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// sweep drops idle buckets. Callers must hold l.mu.
func (l *Limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.seen) > idleTTL {
			delete(l.buckets, k)
		}
	}
}

// Key identifies a login attempt. Both the address and the username are
// included so one attacker cannot lock out every user from a single IP, nor
// spray one user from many IPs unnoticed.
//
// The address comes from the real-IP resolver the router installed, so a
// spoofed X-Forwarded-For cannot shift an attacker onto a fresh bucket unless
// dex was configured to trust that header in the first place.
func Key(ctx context.Context, username string) string {
	ip, _ := ctx.Value(reqctx.RequestKeyRemoteIP).(string)
	return ip + "\x00" + strings.ToLower(username)
}
