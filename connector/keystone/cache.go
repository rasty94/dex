package keystone

import (
	"sync"
	"time"
)

type cacheEntry struct {
	value     interface{}
	expiresAt time.Time
}

type timeCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

func newTimeCache(ttl time.Duration) *timeCache {
	return &timeCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

func (c *timeCache) set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *timeCache) get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.value, true
}
