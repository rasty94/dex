package valkey

import "fmt"

// The topologies a deployment can ask for. The mode is explicit on purpose: it
// could be guessed from the number of addresses, and that guess fails silently.
// Given several addresses that turn out not to form a cluster, valkey-go does
// not fail -- it falls back to standalone against one of them. The operator
// asked for a cluster, got a single node, and nothing said so.
const (
	ModeStandalone = "standalone"
	ModeSentinel   = "sentinel"
	ModeCluster    = "cluster"
)

// Config is the shared store. Empty Addresses keeps every caller on its own
// in-process state, which is the default and needs no server at all.
type Config struct {
	// Mode is standalone, sentinel or cluster. Empty means standalone.
	Mode string `json:"mode"`
	// Addresses are the data nodes, or the sentinels when Mode is sentinel.
	Addresses []string `json:"addresses"`
	// MasterSet is the name sentinel monitors the master under. Required by,
	// and only meaningful to, sentinel.
	MasterSet string `json:"masterSet"`

	// Sentinels can carry credentials of their own, configured separately from
	// the data nodes. Empty falls back to Username and Password.
	SentinelUsername string `json:"sentinelUsername"`
	SentinelPassword string `json:"sentinelPassword"`

	Username  string    `json:"username"`
	Password  string    `json:"password"`
	DB        int       `json:"db"`
	KeyPrefix string    `json:"keyPrefix"`
	TLS       TLSConfig `json:"tls"`
}

type TLSConfig struct {
	CACert             string `json:"caCert"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

// mode returns the topology, defaulting to standalone.
func (c Config) mode() string {
	if c.Mode == "" {
		return ModeStandalone
	}
	return c.Mode
}

// Validate rejects a configuration that would connect and then misbehave. It
// lives here rather than in cmd/dex so the dashboard, which has no Validate of
// its own, gets the same checks.
func (c Config) Validate() error {
	if len(c.Addresses) == 0 {
		// No shared store at all: the default, and not an error. A mode without
		// addresses is a mistake, though -- somebody meant to configure this.
		if c.Mode != "" {
			return fmt.Errorf("valkey: mode %q needs at least one entry in addresses", c.Mode)
		}
		return nil
	}

	switch c.mode() {
	case ModeStandalone:
		if len(c.Addresses) > 1 {
			return fmt.Errorf("valkey: standalone takes one address, got %d; set mode to %q or %q to use several",
				len(c.Addresses), ModeSentinel, ModeCluster)
		}
	case ModeSentinel:
		if c.MasterSet == "" {
			return fmt.Errorf("valkey: mode %q needs masterSet, the name sentinel monitors the master under", ModeSentinel)
		}
	case ModeCluster:
		if c.DB != 0 {
			// It would connect and then fail on every command.
			return fmt.Errorf("valkey: a cluster has no database but 0, got db %d", c.DB)
		}
	default:
		return fmt.Errorf("valkey: unknown mode %q, want %q, %q or %q",
			c.Mode, ModeStandalone, ModeSentinel, ModeCluster)
	}
	return nil
}
