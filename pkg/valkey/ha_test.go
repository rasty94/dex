package valkey

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// addrsFrom reads a comma-separated list from the environment, skipping the test
// when it is not set. This follows the pattern storage/sql already uses with
// DEX_MYSQL_HOST: the suite runs everywhere, and grows teeth where the stack is up.
func addrsFrom(t *testing.T, env string) []string {
	t.Helper()
	raw := os.Getenv(env)
	if raw == "" {
		t.Skipf("%s not set; start docker-compose.valkey-ha.yaml to run this", env)
	}
	return strings.Split(raw, ",")
}

func TestSentinelResolvesTheMaster(t *testing.T) {
	c, err := New(t.Context(), Config{
		Mode:      ModeSentinel,
		Addresses: addrsFrom(t, "DEX_VALKEY_SENTINEL_ADDRS"),
		MasterSet: "dex",
		KeyPrefix: "dextest:",
	})
	if err != nil {
		t.Fatalf("connect through sentinel: %v", err)
	}
	defer c.Close()

	// Writing proves it found the master and not a replica: a replica refuses
	// writes with READONLY.
	if err := c.Do(t.Context(), c.B().Set().Key(c.Key("probe")).Value("v").Ex(time.Minute).Build()).Error(); err != nil {
		t.Errorf("write through sentinel: %v", err)
	}
}

func TestClusterRoutesByKey(t *testing.T) {
	c, err := New(t.Context(), Config{
		Mode:      ModeCluster,
		Addresses: addrsFrom(t, "DEX_VALKEY_CLUSTER_ADDRS"),
		KeyPrefix: "dextest:",
	})
	if err != nil {
		t.Fatalf("connect to the cluster: %v", err)
	}
	defer c.Close()

	// Enough keys to land in different slots on different nodes. Getting them
	// all back proves the client follows the MOVED redirections.
	w := NewFixedWindow(c, "rl")
	for i := 0; i < 32; i++ {
		key := "probe-" + strconv.Itoa(i)
		if n, err := w.Incr(t.Context(), key, time.Minute); err != nil || n != 1 {
			t.Fatalf("key %q across the cluster = %d, %v; want 1, nil", key, n, err)
		}
	}
}
