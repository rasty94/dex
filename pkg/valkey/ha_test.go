package valkey

import (
	"os"
	"os/exec"
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

// The point of sentinel: kill the master and the shared budget survives. Without
// this the rest only proves that the client can connect.
//
// Not run by default even with the stack up: it stops a container and takes
// about fifteen seconds.
func TestTheBudgetSurvivesAFailover(t *testing.T) {
	if os.Getenv("DEX_VALKEY_FAILOVER_TEST") == "" {
		t.Skip("DEX_VALKEY_FAILOVER_TEST not set; this one stops a container")
	}
	ctx := t.Context()

	c, err := New(ctx, Config{
		Mode:      ModeSentinel,
		Addresses: addrsFrom(t, "DEX_VALKEY_SENTINEL_ADDRS"),
		MasterSet: "dex",
		KeyPrefix: "dextest:",
	})
	if err != nil {
		t.Fatalf("connect through sentinel: %v", err)
	}
	defer c.Close()

	w := NewFixedWindow(c, "rl")
	key := "failover-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	for i := int64(1); i <= 3; i++ {
		if n, err := w.Incr(ctx, key, 10*time.Minute); err != nil || n != i {
			t.Fatalf("attempt %d = %d, %v; want %d, nil", i, n, err, i)
		}
	}

	// Stop the master. Sentinel promotes the replica within
	// down-after-milliseconds plus failover-timeout.
	out, err := exec.Command("docker", "compose", "-f", "../../docker-compose.valkey-ha.yaml",
		"--profile", "sentinel", "stop", "valkey-master").CombinedOutput()
	if err != nil {
		t.Fatalf("stop the master: %v: %s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-f", "../../docker-compose.valkey-ha.yaml",
			"--profile", "sentinel", "start", "valkey-master").Run()
	})

	// The count has to carry on from where it was, against the new master. It
	// may take a few tries while the promotion happens.
	deadline := time.Now().Add(30 * time.Second)
	for {
		n, err := w.Incr(ctx, key, 10*time.Minute)
		if err == nil {
			if n != 4 {
				t.Fatalf("after the failover the count was %d, want 4: the budget was reset, which hands an attacker a fresh one", n)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no promotion within 30s: %v", err)
		}
		time.Sleep(time.Second)
	}
}
