package valkey

import (
	"context"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

// fixedWindow counts one attempt and, when it is the first of a window, gives
// the key its lifetime. Both in one script: doing it in two commands leaves a
// counter with no expiry if the process dies in between, and that key would
// lock the user out until someone noticed.
var fixedWindow = valkeygo.NewLuaScript(`
local n = redis.call("INCR", KEYS[1])
if n == 1 then redis.call("PEXPIRE", KEYS[1], ARGV[1]) end
return n
`)

// FixedWindow is a counter shared by every replica, used to throttle attempts.
//
// It is not a token bucket: x/time/rate keeps its state in process and cannot
// be shared. A fixed window allows up to 2 x limit across a window boundary,
// which is the accepted trade for a login throttle and is correct across
// replicas -- the point of having it at all.
//
// One key per attempt, so it is safe in cluster mode: every command it issues
// touches a single key and therefore a single slot.
type FixedWindow struct {
	c    *Client
	kind string
}

// NewFixedWindow returns a counter whose keys are namespaced under kind, so two
// throttles sharing a server do not share a budget.
func NewFixedWindow(c *Client, kind string) *FixedWindow {
	return &FixedWindow{c: c, kind: kind}
}

// Incr counts one attempt against key and returns the running total for the
// current window.
func (w *FixedWindow) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	k := w.c.HashKey(w.kind, key)
	ms := strconv.FormatInt(window.Milliseconds(), 10)
	return fixedWindow.Exec(ctx, w.c.Client, []string{k}, []string{ms}).AsInt64()
}

// Reset forgets the attempts recorded for key.
func (w *FixedWindow) Reset(ctx context.Context, key string) error {
	return w.c.Do(ctx, w.c.B().Del().Key(w.c.HashKey(w.kind, key)).Build()).Error()
}
