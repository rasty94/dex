package ratelimit

import (
	"context"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
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

// sharedCounter is the fixed window kept in Valkey.
//
// This is not the token bucket used locally: x/time/rate keeps its state in
// process and cannot be shared. A fixed window allows up to 2 x attempts across
// a window boundary, which is the accepted trade for a login throttle and is
// correct across replicas -- the point of having it at all.
type sharedCounter struct {
	c *dexvalkey.Client
}

func (s *sharedCounter) incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	k := s.c.HashKey("rl", key)
	ms := strconv.FormatInt(window.Milliseconds(), 10)
	return fixedWindow.Exec(ctx, s.c.Client, []string{k}, []string{ms}).AsInt64()
}

func (s *sharedCounter) reset(ctx context.Context, key string) error {
	k := s.c.HashKey("rl", key)
	return s.c.Do(ctx, s.c.B().Del().Key(k).Build()).Error()
}
