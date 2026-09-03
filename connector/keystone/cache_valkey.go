package keystone

import (
	"context"
	"encoding/json"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/dexidp/dex/connector"
	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

// valkeyCache holds identities where every replica can see them.
//
// The key is a hash of the Keystone token, never the token itself: this store
// may be shared with other services, and key names are the first thing anyone
// with access sees. The value is the identity as JSON -- personal data, which is
// why the deployment documentation says what lives in here.

// opTimeout bounds a single call to the shared store. Without a deadline on
// ctx, the valkey client retries a downed server forever, and this cache
// would hang every login instead of missing fast the way the rest of this
// package promises.
const opTimeout = 2 * time.Second

type valkeyCache struct {
	c   *dexvalkey.Client
	ttl time.Duration
}

func newValkeyCache(c *dexvalkey.Client, ttl time.Duration) *valkeyCache {
	return &valkeyCache{c: c, ttl: ttl}
}

func (v *valkeyCache) get(ctx context.Context, token string) (connector.Identity, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	raw, err := v.c.Do(ctx, v.c.B().Get().Key(v.c.HashKey("tok", token)).Build()).AsBytes()
	if err != nil {
		// A key that is not there and a Valkey that cannot be reached both mean
		// the login goes on to Keystone -- the optimization being unavailable
		// never fails a login. They are reported apart so that an outage does
		// not hide behind a plausible-looking miss rate.
		if valkeygo.IsValkeyNil(err) {
			return connector.Identity{}, false, nil
		}
		return connector.Identity{}, false, err
	}
	var id connector.Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		// Something else wrote this key, or wrote it in an older shape. Treat it
		// as absent; it will be overwritten by the next successful login.
		return connector.Identity{}, false, nil
	}
	return id, true, nil
}

func (v *valkeyCache) set(ctx context.Context, token string, id connector.Identity) {
	raw, err := json.Marshal(id)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	// Px, not Ex: Ex truncates to whole seconds, which turns any cacheTTL under
	// a second into "SET ... EX 0" -- rejected by Valkey, so the entry would
	// silently never populate.
	_ = v.c.Do(ctx, v.c.B().Set().
		Key(v.c.HashKey("tok", token)).
		Value(string(raw)).
		Px(v.ttl).
		Build()).Error()
}
