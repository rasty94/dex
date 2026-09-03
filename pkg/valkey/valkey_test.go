package valkey

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// An empty address is how an operator says "keep everything in memory". It has
// to be a nil client and not an error, because every caller treats nil as
// "there is no shared store" and falls back to its local implementation.
func TestNoAddressMeansNoClient(t *testing.T) {
	c, err := New(t.Context(), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c != nil {
		t.Fatal("an empty address must not open a connection")
	}
}

func TestNewPingsAndPrefixes(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Address: m.Addr(), KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if got := c.Key("rl:abc"); got != "dex:rl:abc" {
		t.Errorf("Key = %q, want dex:rl:abc", got)
	}
}

// The key must not carry the secret it identifies: a Valkey shared with other
// services would otherwise list live tokens and usernames as key names.
func TestHashKeyHidesTheSecret(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Address: m.Addr(), KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	const secret = "gAAAAABlive-keystone-token"
	got := c.HashKey("tok", secret)

	if strings.Contains(got, secret) {
		t.Errorf("the key carries the secret verbatim: %q", got)
	}
	if !strings.HasPrefix(got, "dex:tok:") {
		t.Errorf("HashKey = %q, want the dex:tok: prefix", got)
	}
	if got != c.HashKey("tok", secret) {
		t.Error("HashKey is not stable, so a lookup could never hit")
	}
}

// A configured address that does not answer is a startup error, not a silent
// fallback: an operator who asked for a shared store must not discover during
// an incident that it was never shared.
func TestUnreachableAddressFailsToStart(t *testing.T) {
	m := miniredis.RunT(t)
	addr := m.Addr()
	m.Close()

	if _, err := New(context.Background(), Config{Address: addr}); err == nil {
		t.Fatal("an unreachable address must fail to start")
	}
}

// Every key dex writes here has an expiry, so volatile-* is no safer than
// allkeys-*: both target exactly what dex stores. The counters of the login
// limiter are the ones that matter -- evicting them hands out a fresh budget.
func TestEvictionRisk(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]string
		want bool
	}{
		{"no limit, so nothing is ever evicted", map[string]string{"maxmemory": "0", "maxmemory-policy": "allkeys-lru"}, false},
		{"limit with noeviction refuses writes instead", map[string]string{"maxmemory": "1073741824", "maxmemory-policy": "noeviction"}, false},
		{"allkeys-lru drops any key", map[string]string{"maxmemory": "1073741824", "maxmemory-policy": "allkeys-lru"}, true},
		{"volatile-ttl targets the keys with an expiry, which is all of dex's", map[string]string{"maxmemory": "1073741824", "maxmemory-policy": "volatile-ttl"}, true},
		{"a server that answered nothing useful is not accused", map[string]string{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := evictionRisk(tc.cfg); got != tc.want {
				t.Errorf("evictionRisk(%v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}
