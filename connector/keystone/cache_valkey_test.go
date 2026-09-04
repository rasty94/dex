package keystone

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/dexidp/dex/connector"
	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

func sharedCache(t *testing.T, addr string, ttl time.Duration) *valkeyCache {
	t.Helper()
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Addresses: []string{addr}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)
	return newValkeyCache(c, ttl)
}

// One replica validates a token against Keystone; the others must not have to.
func TestASecondReplicaHitsWhatTheFirstCached(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := sharedCache(t, m.Addr(), time.Minute)
	b := sharedCache(t, m.Addr(), time.Minute)

	want := connector.Identity{UserID: "u-1", Email: "jane@example.com", Groups: []string{"admins"}}
	a.set(ctx, "keystone-token", want)

	got, ok, _ := b.get(ctx, "keystone-token")
	if !ok {
		t.Fatal("the second replica missed what the first stored")
	}
	if got.UserID != want.UserID || got.Email != want.Email || len(got.Groups) != 1 {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The raw token must never be a key name: a Valkey shared with other services
// would be listing live credentials.
func TestTheTokenIsNotAKeyName(t *testing.T) {
	m := miniredis.RunT(t)
	c := sharedCache(t, m.Addr(), time.Minute)

	c.set(t.Context(), "gAAAAAB-live-token", connector.Identity{UserID: "u"})

	for _, k := range m.Keys() {
		if k == "gAAAAAB-live-token" || k == "dex:gAAAAAB-live-token" {
			t.Fatalf("the raw token is a key name: %q", k)
		}
	}
}

func TestTheSharedEntryExpires(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c := sharedCache(t, m.Addr(), time.Minute)
	c.set(ctx, "tok", connector.Identity{UserID: "u"})

	m.FastForward(time.Minute + time.Second)
	if _, ok, _ := c.get(ctx, "tok"); ok {
		t.Error("an expired entry was served")
	}
}

// Ex truncates to whole seconds, so a sub-second cacheTTL used to become
// "SET ... EX 0" -- rejected by Valkey, silently, since set discards the
// error. Px keeps millisecond precision, so the entry must still be there
// right after it is set.
func TestASubSecondTTLRoundTrips(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c := sharedCache(t, m.Addr(), 500*time.Millisecond)
	c.set(ctx, "tok", connector.Identity{UserID: "u"})

	got, ok, _ := c.get(ctx, "tok")
	if !ok {
		t.Fatal("a sub-second TTL entry vanished instead of round-tripping")
	}
	if got.UserID != "u" {
		t.Errorf("got %+v, want UserID %q", got, "u")
	}
}

// A cache is an optimization. If Valkey is gone the login still has to work, so
// every failure is a miss and never an error.
// A dead Valkey must not fail a login -- the cache is an optimization -- but it
// must not pass for a cold cache either: the connector counts the two apart so
// that an outage is visible instead of hiding in the miss rate.
func TestValkeyDownIsReportedAndStillLetsTheLoginThrough(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c := sharedCache(t, m.Addr(), time.Minute)
	c.set(ctx, "tok", connector.Identity{UserID: "u"})
	m.Close()

	_, ok, err := c.get(ctx, "tok")
	if ok {
		t.Error("a dead Valkey reported a hit")
	}
	if err == nil {
		t.Error("a dead Valkey looked like a key that was simply not there")
	}
	c.set(ctx, "tok", connector.Identity{UserID: "u"}) // must not panic
}

// A key that is genuinely absent is not an error: it is the ordinary first time
// a token is seen, and reporting it as a failure would cry wolf on every new
// login.
func TestAnAbsentKeyIsNotAnError(t *testing.T) {
	m := miniredis.RunT(t)

	c := sharedCache(t, m.Addr(), time.Minute)
	_, ok, err := c.get(t.Context(), "never-cached")
	if ok {
		t.Error("an empty cache reported a hit")
	}
	if err != nil {
		t.Errorf("a token that was never cached reported an error: %v", err)
	}
}
