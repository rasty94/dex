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
	"os"

	valkeygo "github.com/valkey-io/valkey-go"
)

// Config is the shared store. An empty Address keeps every caller on its own
// in-process state, which is the default and needs no server at all.
type Config struct {
	Address   string    `json:"address"`
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

// Client is the shared connection. The valkey-go client is embedded, so callers
// build commands with c.B() and run them with c.Do() as usual.
//
// A nil *Client means "no shared store": every caller checks for it and uses its
// own in-memory implementation instead. Do not call methods on a nil Client.
type Client struct {
	valkeygo.Client

	prefix string
}

// New opens and verifies the connection. An empty address returns (nil, nil):
// that is the configuration saying everything stays in memory.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Address == "" {
		return nil, nil
	}

	opt := valkeygo.ClientOption{
		InitAddress: []string{cfg.Address},
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
