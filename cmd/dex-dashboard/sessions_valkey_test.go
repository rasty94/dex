package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	dexvalkey "github.com/dexidp/dex/pkg/valkey"
)

func valkeySessionsFor(t *testing.T, addr string, ttl, idle time.Duration) *valkeySessions {
	t.Helper()
	return valkeySessionsForWithLogger(t, addr, ttl, idle, testLogger())
}

func valkeySessionsForWithLogger(t *testing.T, addr string, ttl, idle time.Duration, logger *slog.Logger) *valkeySessions {
	t.Helper()
	c, err := dexvalkey.New(t.Context(), dexvalkey.Config{Address: addr, KeyPrefix: "dex-dashboard:"})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(c.Close)
	return newValkeySessions(c, ttl, idle, logger)
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

// A dead store fails closed correctly, but that looks exactly like normal
// session expiry unless something says otherwise. get must warn, so an
// operator has something to find besides "everyone got logged out".
func TestValkeyDownLogsAWarning(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	s := valkeySessionsForWithLogger(t, m.Addr(), time.Hour, 30*time.Minute, logger)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})
	m.Close()

	s.get(ctx, id)

	if !strings.Contains(logs.String(), "valkey session store unreachable") {
		t.Errorf("no warning logged for an unreachable store, got: %s", logs.String())
	}
}

// The warning is rate-limited, or a store that stays down floods the log at
// the same rate as every bounced login.
func TestValkeyDownWarningIsRateLimited(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	s := valkeySessionsForWithLogger(t, m.Addr(), time.Hour, 30*time.Minute, logger)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})
	m.Close()

	fakeNow := time.Now()
	s.now = func() time.Time { return fakeNow }

	s.get(ctx, id)
	s.get(ctx, id)
	s.get(ctx, id)

	if n := strings.Count(logs.String(), "valkey session store unreachable"); n != 1 {
		t.Errorf("got %d warnings for three failures within the rate-limit window, want 1", n)
	}

	fakeNow = fakeNow.Add(31 * time.Second)
	s.get(ctx, id)

	if n := strings.Count(logs.String(), "valkey session store unreachable"); n != 2 {
		t.Errorf("got %d warnings after the rate-limit window passed, want 2", n)
	}
}

// Without a deadline on ctx, valkey-go retries a downed server forever, and
// this call would hang instead of asking for a login. get runs off the main
// goroutine so a regression here fails with a clean t.Fatal instead of hanging
// until go test's own global timeout.
func TestValkeyDownRefusesPromptly(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)
	id, _ := s.create(ctx, &session{Email: "admin@example.com"})
	m.Close()

	done := make(chan bool, 1)
	go func() {
		_, ok := s.get(ctx, id)
		done <- ok
	}()

	select {
	case ok := <-done:
		if ok {
			t.Error("a session was accepted with the store unreachable")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("get did not return within 5s with the store down; opTimeout is not bounding the call")
	}
}

// window() correctly caps the idle refresh by what is left of the absolute
// lifetime, but that path is only exercised here: nothing else moves v.now
// independently of Valkey's own key TTL. Without the cap, a session read
// often enough would never end.
func TestAbsoluteLifetimeCapsAConstantlyUsedSession(t *testing.T) {
	m := miniredis.RunT(t)
	ctx := t.Context()

	s := valkeySessionsFor(t, m.Addr(), time.Hour, 30*time.Minute)

	fakeNow := time.Now()
	s.now = func() time.Time { return fakeNow }

	id, _ := s.create(ctx, &session{Email: "admin@example.com"})

	// Read it steadily, well inside the 30m idle window each time, so only
	// the 1h absolute lifetime can end it.
	for range 2 {
		fakeNow = fakeNow.Add(25 * time.Minute)
		if _, ok := s.get(ctx, id); !ok {
			t.Fatal("session dropped before its absolute lifetime, even though it was in constant use")
		}
	}

	// The third read lands at 75 minutes, past the 1h absolute expiry.
	// Constant use must not make the session immortal.
	fakeNow = fakeNow.Add(25 * time.Minute)
	if _, ok := s.get(ctx, id); ok {
		t.Error("a session in constant use survived its absolute lifetime")
	}
}
