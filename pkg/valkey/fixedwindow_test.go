package valkey

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestFixedWindowCountsAcrossClients(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	newWindow := func() *FixedWindow {
		c, err := New(ctx, Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		t.Cleanup(c.Close)
		return NewFixedWindow(c, "rl")
	}

	a, b := newWindow(), newWindow()

	// Two processes, one budget: that is the whole point.
	if n, err := a.Incr(ctx, "k", time.Minute); err != nil || n != 1 {
		t.Fatalf("first attempt = %d, %v; want 1, nil", n, err)
	}
	if n, err := b.Incr(ctx, "k", time.Minute); err != nil || n != 2 {
		t.Fatalf("second attempt from another client = %d, %v; want 2, nil", n, err)
	}

	// Reset clears it for everyone.
	if err := a.Reset(ctx, "k"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if n, err := b.Incr(ctx, "k", time.Minute); err != nil || n != 1 {
		t.Fatalf("after reset = %d, %v; want 1, nil", n, err)
	}
}

// The window has to expire on its own. Without the PEXPIRE, a counter that
// reached the limit would lock the key out until somebody noticed.
func TestFixedWindowExpires(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c, err := New(ctx, Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(c.Close)
	w := NewFixedWindow(c, "rl")

	if _, err := w.Incr(ctx, "k", time.Minute); err != nil {
		t.Fatalf("incr: %v", err)
	}
	m.FastForward(2 * time.Minute)
	if n, _ := w.Incr(ctx, "k", time.Minute); n != 1 {
		t.Errorf("after the window passed = %d, want the count to start over at 1", n)
	}
}

// Two kinds must not share a budget: the dashboard's own login throttle is not
// dex's.
func TestFixedWindowKindsAreSeparate(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	c, err := New(ctx, Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(c.Close)

	if _, err := NewFixedWindow(c, "rl").Incr(ctx, "k", time.Minute); err != nil {
		t.Fatalf("incr: %v", err)
	}
	if n, _ := NewFixedWindow(c, "dl").Incr(ctx, "k", time.Minute); n != 1 {
		t.Errorf("a different kind counted %d, want its own budget starting at 1", n)
	}
}
