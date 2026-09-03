package keystone

import (
	"context"
	"encoding/json"
	"time"

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

func (v *valkeyCache) get(ctx context.Context, token string) (connector.Identity, bool) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	raw, err := v.c.Do(ctx, v.c.B().Get().Key(v.c.HashKey("tok", token)).Build()).AsBytes()
	if err != nil {
		// Missing key, or Valkey unreachable. Both are a cache miss: a login is
		// never failed because the optimization is unavailable.
		return connector.Identity{}, false
	}
	var id connector.Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return connector.Identity{}, false
	}
	return id, true
}

func (v *valkeyCache) set(ctx context.Context, token string, id connector.Identity) {
	raw, err := json.Marshal(id)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	_ = v.c.Do(ctx, v.c.B().Set().
		Key(v.c.HashKey("tok", token)).
		Value(string(raw)).
		Ex(v.ttl).
		Build()).Error()
}
