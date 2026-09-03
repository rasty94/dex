package valkey

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// An empty address is how an operator says "keep everything in memory". It has
// to be a nil client and not an error, because every caller treats nil as
// "there is no shared store" and falls back to its local implementation.
func TestNoAddressMeansNoClient(t *testing.T) {
	c, err := New(t.Context(), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c != nil {
		t.Fatal("an empty address must not open a connection")
	}
}

func TestNewPingsAndPrefixes(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Address: m.Addr(), KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if got := c.Key("rl:abc"); got != "dex:rl:abc" {
		t.Errorf("Key = %q, want dex:rl:abc", got)
	}
}

// The key must not carry the secret it identifies: a Valkey shared with other
// services would otherwise list live tokens and usernames as key names.
func TestHashKeyHidesTheSecret(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Address: m.Addr(), KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	const secret = "gAAAAABlive-keystone-token"
	got := c.HashKey("tok", secret)

	if strings.Contains(got, secret) {
		t.Errorf("the key carries the secret verbatim: %q", got)
	}
	if !strings.HasPrefix(got, "dex:tok:") {
		t.Errorf("HashKey = %q, want the dex:tok: prefix", got)
	}
	if got != c.HashKey("tok", secret) {
		t.Error("HashKey is not stable, so a lookup could never hit")
	}
}

// A configured address that does not answer is a startup error, not a silent
// fallback: an operator who asked for a shared store must not discover during
// an incident that it was never shared.
func TestUnreachableAddressFailsToStart(t *testing.T) {
	m := miniredis.RunT(t)
	addr := m.Addr()
	m.Close()

	if _, err := New(context.Background(), Config{Address: addr}); err == nil {
		t.Fatal("an unreachable address must fail to start")
	}
}
