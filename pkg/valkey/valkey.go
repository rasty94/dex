// Package valkey opens the connection dex shares with its replicas. It is not a
// cache abstraction: each caller uses the commands its own problem needs, and
// this package only owns the connection, the key prefix, and the rule that no
// key ever carries a secret in its name.
package valkey

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

// Client is the shared connection. The valkey-go client is embedded, so callers
// build commands with c.B() and run them with c.Do() as usual.
//
// A nil *Client means "no shared store": every caller checks for it and uses its
// own in-memory implementation instead. Do not call methods on a nil Client.
type Client struct {
	valkeygo.Client

	prefix string
}

// New opens and verifies the connection. Empty addresses return (nil, nil):
// that is the configuration saying everything stays in memory.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if len(cfg.Addresses) == 0 {
		return nil, nil
	}

	opt := valkeygo.ClientOption{
		InitAddress: cfg.Addresses,
		Username:    cfg.Username,
		Password:    cfg.Password,
		SelectDB:    cfg.DB,
		// Every caller here reads and writes its own keys directly, so
		// client-side caching buys nothing. Disabling it also keeps this
		// package usable against miniredis in tests, which does not
		// implement the server-assisted invalidation tracking it needs.
		DisableCache: true,
	}
	if cfg.TLS.CACert != "" || cfg.TLS.InsecureSkipVerify {
		tlsCfg := &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		}
		if cfg.TLS.CACert != "" {
			pem, err := os.ReadFile(cfg.TLS.CACert)
			if err != nil {
				return nil, fmt.Errorf("read valkey caCert: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("valkey caCert %q holds no certificate", cfg.TLS.CACert)
			}
			tlsCfg.RootCAs = pool
		}
		opt.TLSConfig = tlsCfg
	}

	c, err := valkeygo.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("connect to valkey: %w", err)
	}
	// Prove it answers now rather than on the first login.
	if err := c.Do(ctx, c.B().Ping().Build()).Error(); err != nil {
		c.Close()
		return nil, fmt.Errorf("ping valkey: %w", err)
	}
	return &Client{Client: c, prefix: cfg.KeyPrefix}, nil
}

// Key namespaces a key that carries nothing secret.
func (c *Client) Key(name string) string {
	return c.prefix + name
}

// HashKey namespaces a key derived from something that must not appear in the
// store: a Keystone token, a username, a session id.
func (c *Client) HashKey(kind, secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return c.prefix + kind + ":" + hex.EncodeToString(sum[:])
}

// WarnIfKeysCanBeEvicted says so when the server is allowed to drop dex's keys
// on its own.
//
// Everything dex keeps here has an expiry, and that is the trap: under a
// maxmemory limit, every policy except noeviction is free to remove keys early,
// and the volatile-* ones target precisely the keys that have a TTL -- all of
// dex's. What goes with them is the login rate limiter's counters, so an
// attacker gets a fresh budget handed over whenever the server is under memory
// pressure, and the administrator sessions of the dashboard.
//
// Only a warning: this may be somebody else's server, and dex is not the one to
// decide how it is run. Being unable to ask is not reported at all -- CONFIG is
// often disabled or renamed on a managed service, and a warning nobody can act
// on is noise.
func (c *Client) WarnIfKeysCanBeEvicted(ctx context.Context, logger *slog.Logger) {
	// The client retries a read against a dead server for as long as it is
	// allowed to, and this one runs while dex is still starting up. A server
	// that stopped answering between the ping above and this call must not
	// leave the process hanging before it ever serves a request.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cfg, err := c.Do(ctx, c.B().ConfigGet().Parameter("maxmemory").Parameter("maxmemory-policy").Build()).AsStrMap()
	if err != nil {
		return
	}
	if policy, risky := evictionRisk(cfg); risky {
		logger.Warn("valkey may evict keys under memory pressure, and everything dex keeps there has an expiry, so it is exactly what a volatile-* policy targets. What disappears with it is a login attempt budget or a signed-in session. Use maxmemory-policy noeviction, or give dex a server of its own",
			"maxmemory_policy", policy, "maxmemory", cfg["maxmemory"])
	}
}

// evictionRisk reports whether this server's memory settings let it discard keys
// that dex expects to still be there. Split out from the call above so the rule
// can be tested without a server that answers CONFIG.
func evictionRisk(cfg map[string]string) (policy string, risky bool) {
	policy = cfg["maxmemory-policy"]
	// Without a memory ceiling nothing is ever evicted, whatever the policy says.
	if limit := cfg["maxmemory"]; limit == "" || limit == "0" {
		return policy, false
	}
	// noeviction refuses writes instead of dropping keys, which is the behavior
	// dex needs: a login that cannot be counted is better than one that is
	// counted against a budget somebody else's memory pressure just reset.
	return policy, policy != "" && policy != "noeviction"
}
