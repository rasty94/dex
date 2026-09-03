package main

import (
	"context"
	"encoding/json"
	"time"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

// opTimeout bounds a single call to the shared store. Without a deadline on
// ctx, the valkey client retries a downed server forever, and session lookup
// would hang every request instead of failing closed into a login.
const opTimeout = 2 * time.Second

// valkeySessions keeps administrator sessions where every replica sees them.
//
// The idle timeout is the key's TTL, refreshed on each read, so there is no
// LastSeen to write back on every request. The absolute lifetime caps that
// refresh: a session in constant use still ends when its Expiry passes.
//
// What is stored decides who may change things -- CanWrite and Groups travel in
// here -- so write access to this store is write access to the panel. The
// deployment documentation says so next to the address.
type valkeySessions struct {
	c       *dexvalkey.Client
	ttl     time.Duration
	idleTTL time.Duration
	now     func() time.Time
}

func newValkeySessions(c *dexvalkey.Client, ttl, idleTTL time.Duration) *valkeySessions {
	return &valkeySessions{c: c, ttl: ttl, idleTTL: idleTTL, now: time.Now}
}

// window is how long the key should live from now: the idle timeout, or what is
// left of the absolute lifetime when that is shorter.
func (v *valkeySessions) window(sess *session) time.Duration {
	remaining := sess.Expiry.Sub(v.now())
	if remaining <= 0 {
		return 0
	}
	if v.idleTTL > 0 && v.idleTTL < remaining {
		return v.idleTTL
	}
	return remaining
}

func (v *valkeySessions) create(ctx context.Context, sess *session) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	id, err := randomToken()
	if err != nil {
		return "", err
	}
	now := v.now()
	sess.Expiry = now.Add(v.ttl)
	sess.AuthAt = now
	sess.LastSeen = now

	raw, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	if err := v.c.Do(ctx, v.c.B().Set().
		Key(v.c.HashKey("sess", id)).
		Value(string(raw)).
		Ex(v.window(sess)).
		Build()).Error(); err != nil {
		return "", err
	}
	return id, nil
}

func (v *valkeySessions) get(ctx context.Context, id string) (*session, bool) {
	key := v.c.HashKey("sess", id)

	getCtx, cancel := context.WithTimeout(ctx, opTimeout)
	raw, err := v.c.Do(getCtx, v.c.B().Get().Key(key).Build()).AsBytes()
	cancel()
	if err != nil {
		// Unknown id, or the store is unreachable. Either way the panel cannot
		// vouch for this session, so it asks for a login.
		return nil, false
	}
	var sess session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, false
	}

	w := v.window(&sess)
	if w <= 0 {
		v.delete(ctx, id)
		return nil, false
	}
	// Push the idle window out, capped by the absolute expiry. Its own
	// timeout, separate from the GET above, so a slow read cannot starve this
	// call of budget and silently skip the refresh while still reporting ok.
	pexpireCtx, pcancel := context.WithTimeout(ctx, opTimeout)
	_ = v.c.Do(pexpireCtx, v.c.B().Pexpire().Key(key).Milliseconds(w.Milliseconds()).Build()).Error()
	pcancel()

	sess.LastSeen = v.now()
	return &sess, true
}

func (v *valkeySessions) delete(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	_ = v.c.Do(ctx, v.c.B().Del().Key(v.c.HashKey("sess", id)).Build()).Error()
}
