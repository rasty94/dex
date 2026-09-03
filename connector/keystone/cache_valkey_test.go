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
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Address: addr, KeyPrefix: "dex:"})
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

	got, ok := b.get(ctx, "keystone-token")
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
	if _, ok := c.get(ctx, "tok"); ok {
		t.Error("an expired entry was served")
	}
}

// A cache is an optimization. If Valkey is gone the login still has to work, so
// every failure is a miss and never an error.
func TestValkeyDownIsAMissAndNotAnError(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c := sharedCache(t, m.Addr(), time.Minute)
	c.set(ctx, "tok", connector.Identity{UserID: "u"})
	m.Close()

	if _, ok := c.get(ctx, "tok"); ok {
		t.Error("a dead Valkey reported a hit")
	}
	c.set(ctx, "tok", connector.Identity{UserID: "u"}) // must not panic
}
