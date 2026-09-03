package keystone

import (
	"context"
	"sync"
	"time"

	"github.com/dexidp/dex/connector"
)

// identityCache is what the connector needs from a cache: an identity for a
// Keystone token. Both the in-process cache and the shared one satisfy it.
type identityCache interface {
	get(ctx context.Context, token string) (connector.Identity, bool)
	set(ctx context.Context, token string, id connector.Identity)
}

type cacheEntry struct {
	value     connector.Identity
	expiresAt time.Time
}

// timeCache holds identities in this process. Entries are dropped on write
// rather than only ignored on read: Keystone tokens rotate, so a cache that
// never deletes grows for as long as the process runs.
type timeCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
	now     func() time.Time
}

func newTimeCache(ttl time.Duration) *timeCache {
	return &timeCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
		now:     time.Now,
	}
}

func (c *timeCache) set(_ context.Context, key string, value connector.Identity) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.sweep(now)
	c.entries[key] = cacheEntry{
		value:     value,
		expiresAt: now.Add(c.ttl),
	}
}

func (c *timeCache) get(_ context.Context, key string) (connector.Identity, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok || c.now().After(entry.expiresAt) {
		return connector.Identity{}, false
	}
	return entry.value, true
}

// sweep drops expired entries. Callers must hold c.mu.
func (c *timeCache) sweep(now time.Time) {
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}

// len reports how many entries are held, for the tests that check the sweep.
func (c *timeCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
