package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
)

// Entries idle for longer than this are dropped on the next sweep, so a burst
// of attempts from many addresses does not leak memory.
const loginLimiterIdleTTL = 30 * time.Minute

// loginLimiter throttles password login attempts before they reach the upstream
// identity provider, keyed by client IP and username. Only failed attempts
// count: a successful login clears the counter for that key, so a legitimate
// user (or a service using the password grant) is never throttled.
//
// ponytail: in-process state, so with several dex replicas the effective limit
// is Attempts × replicas. Move the buckets to the storage backend or Redis if
// that ceiling matters.
type loginLimiter struct {
	limit rate.Limit
	burst int
	now   func() time.Time

	// Counts refused attempts. Without it there is no way to tell a limit that
	// is stopping an attack from one that is locking real users out.
	rejected prometheus.Counter

	mu      sync.Mutex
	buckets map[string]*loginBucket
}

type loginBucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// newLoginLimiter allows attempts failures per window, per IP and username.
// It returns nil when rate limiting is disabled; a nil limiter allows everything.
func newLoginLimiter(cfg LoginRateLimitConfig, now func() time.Time) *loginLimiter {
	if !cfg.Enabled {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{
		limit:   rate.Every(cfg.Window / time.Duration(cfg.Attempts)),
		burst:   cfg.Attempts,
		now:     now,
		buckets: make(map[string]*loginBucket),
	}
}

// allow reports whether another login attempt may be made for key.
func (l *loginLimiter) allow(key string) bool {
	if l == nil {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.sweep(now)
		b = &loginBucket{limiter: rate.NewLimiter(l.limit, l.burst)}
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

// reset forgets the failed attempts recorded for key.
func (l *loginLimiter) reset(key string) {
	if l == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.buckets, key)
}

// sweep drops idle buckets. Callers must hold l.mu.
func (l *loginLimiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.seen) > loginLimiterIdleTTL {
			delete(l.buckets, k)
		}
	}
}

// loginKey identifies a login attempt for rate limiting purposes. Both the
// address and the username are included so one attacker cannot lock out every
// user from a single IP, nor spray one user from many IPs unnoticed.
func loginKey(r *http.Request, username string) string {
	return clientIP(r) + "\x00" + strings.ToLower(username)
}

// clientIP returns the address the request came from, preferring the first hop
// in X-Forwarded-For when dex sits behind a proxy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, found := strings.Cut(xff, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
