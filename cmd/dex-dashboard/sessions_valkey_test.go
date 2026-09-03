package main

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

func valkeySessionsFor(t *testing.T, addr string, ttl, idle time.Duration) *valkeySessions {
	t.Helper()
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Address: addr, KeyPrefix: "dex-dashboard:"})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)
	return newValkeySessions(c, ttl, idle)
}

// A replicated panel must not sign an administrator out just because the load
// balancer moved them to another instance.
func TestASessionCreatedOnOneReplicaWorksOnAnother(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	b := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)

	id, err := a.create(ctx, &session{Email: "admin@example.com", CanWrite: true, Groups: []string{"dex-admins"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, ok := b.get(ctx, id)
	if !ok {
		t.Fatal("the second replica did not know the session")
	}
	if got.Email != "admin@example.com" || !got.CanWrite {
		t.Errorf("session came back as %+v", got)
	}
}

// The write permission travels through the store, so it has to survive the round
// trip exactly: a session that loses CanWrite locks an administrator out, and one
// that gains it is a privilege escalation.
func TestWritePermissionSurvivesTheRoundTrip(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, err := s.create(ctx, &session{Email: "viewer@example.com", CanWrite: false})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, ok := s.get(ctx, id)
	if !ok {
		t.Fatal("session not found")
	}
	if got.CanWrite {
		t.Error("a read-only session came back with write permission")
	}
}

// The idle timeout is what makes an abandoned console stop working. Valkey keeps
// it, so a session left alone past the idle window must be gone.
func TestIdleSessionsExpire(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, err := s.create(ctx, &session{Email: "admin@example.com"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	m.FastForward(31 * time.Minute)
	if _, ok := s.get(ctx, id); ok {
		t.Error("an idle session survived its window")
	}
}

// Reading refreshes the idle window, or an administrator working steadily would
// be logged out mid-task.
func TestUsingASessionKeepsItAlive(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})

	for range 3 {
		m.FastForward(20 * time.Minute)
		if _, ok := s.get(ctx, id); !ok {
			t.Fatal("a session in continuous use was dropped")
		}
	}
}

func TestDeleteEndsTheSessionEverywhere(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	a := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	b := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)

	id, _ := a.create(ctx, &session{Email: "admin@example.com"})
	a.delete(ctx, id)

	if _, ok := b.get(ctx, id); ok {
		t.Error("logging out on one replica left the session valid on another")
	}
}

// With the store gone the panel cannot tell a real session from an invented one,
// so it must refuse. Failing closed here costs a login; failing open costs the
// panel.
func TestValkeyDownRefusesTheSession(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})
	m.Close()

	if _, ok := s.get(ctx, id); ok {
		t.Error("a session was accepted with the store unreachable")
	}
}

// Without a deadline on ctx, valkey-go retries a downed server forever, and
// this call would hang instead of asking for a login. Asserting on elapsed
// time makes a regression here fail with a clean assertion instead of hanging
// until go test's own global timeout.
func TestValkeyDownRefusesPromptly(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})
	m.Close()

	start := time.Now()
	_, ok := s.get(ctx, id)
	elapsed := time.Since(start)

	if ok {
		t.Error("a session was accepted with the store unreachable")
	}
	if elapsed > 5*time.Second {
		t.Errorf("get took %s with the store down; want well under 5s", elapsed)
	}
}
