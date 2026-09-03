package valkey

import "sync/atomic"

// shared is the process-wide client, set once at startup.
//
// ponytail: package-level state, which is the price of connector.Open(id,
// logger) having nowhere to inject a dependency. The alternative was giving the
// Keystone connector its own valkey block, and connector configuration is
// stored in the database — the password would end up in the connector store and
// in the dashboard's edit form.
var shared atomic.Pointer[Client]

// SetShared publishes the client for components that cannot be handed one.
func SetShared(c *Client) { shared.Store(c) }

// Shared returns the client set at startup, or nil when none was configured.
func Shared() *Client { return shared.Load() }
