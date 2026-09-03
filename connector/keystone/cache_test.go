package keystone

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dexidp/dex/connector"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The cache checked expiry on read but never deleted, so an entry for a token
// that is never seen again stayed forever. Keystone tokens rotate, so that is
// unbounded growth in a long-running process -- with a single replica.
func TestExpiredEntriesAreDropped(t *testing.T) {
	c := newTimeCache(time.Minute)
	ctx := t.Context()

	for i := range 100 {
		c.set(ctx, "token-"+string(rune('a'+i%26))+string(rune('a'+i/26)), connector.Identity{UserID: "u"})
	}
	if n := c.len(); n != 100 {
		t.Fatalf("stored %d entries, want 100", n)
	}

	c.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	c.set(ctx, "fresh", connector.Identity{UserID: "u"})

	if n := c.len(); n != 1 {
		t.Errorf("%d entries survived their TTL; only the fresh one should remain", n)
	}
}

func TestCacheRoundTripsAnIdentity(t *testing.T) {
	c := newTimeCache(time.Minute)
	ctx := t.Context()

	want := connector.Identity{UserID: "u-1", Email: "jane@example.com", Groups: []string{"admins"}}
	c.set(ctx, "tok", want)

	got, ok, _ := c.get(ctx, "tok")
	if !ok {
		t.Fatal("the entry just written was not found")
	}
	if got.UserID != want.UserID || got.Email != want.Email || len(got.Groups) != 1 {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A misspelled cacheTTL used to disable the cache and say nothing, so an
// operator who asked for caching got none and had no way to notice.
func TestAMisspelledCacheTTLIsRefused(t *testing.T) {
	_, err := (&Config{Host: "http://keystone", CacheTTL: "5min"}).Open("kc", testLogger())
	if err == nil {
		t.Fatal("a cacheTTL that does not parse was accepted")
	}
	if !strings.Contains(err.Error(), "cacheTTL") {
		t.Errorf("the error does not name the field: %v", err)
	}
}

func TestExpiredEntryIsAMiss(t *testing.T) {
	c := newTimeCache(time.Minute)
	ctx := t.Context()

	c.set(ctx, "tok", connector.Identity{UserID: "u"})
	c.now = func() time.Time { return time.Now().Add(2 * time.Minute) }

	if _, ok, _ := c.get(ctx, "tok"); ok {
		t.Error("an expired entry was served")
	}
}
