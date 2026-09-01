// Package ratelimit throttles password login attempts before they reach the
// upstream identity provider. It lives in its own package so both the
// interactive auth flow and the password grant can share one limiter without
// an import cycle.
package ratelimit

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

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
// ponytail: in-process state, so with several dex replicas the effective limit
// is Attempts × replicas. Move the buckets to the storage backend or Redis if
// that ceiling matters.
type Limiter struct {
	limit rate.Limit
	burst int
	now   func() time.Time

	// Counts refused attempts. Without it there is no way to tell a limit that
	// is stopping an attack from one that is locking real users out.
	rejected prometheus.Counter

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

// Allow reports whether another login attempt may be made for key.
func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}

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
func (l *Limiter) Reset(key string) {
	if l == nil {
		return
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
