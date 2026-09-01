package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var testTokens = []GRPCToken{
	{Name: "default", Token: "legacy-secret"},
	{Name: "dashboard", Token: "dashboard-secret"},
	{Name: "ci", Token: "ci-secret"},
}

// ctxWithMD builds an incoming context the way gRPC delivers one to a server
// interceptor. An empty key means "send no metadata at all".
func ctxWithMD(key, value string) context.Context {
	if key == "" {
		return context.Background()
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(key, value))
}

func TestAuthInterceptor(t *testing.T) {
	tests := []struct {
		name       string
		mdKey      string
		mdValue    string
		wantCaller string // empty means the call must be rejected
	}{
		{name: "no metadata", mdKey: "", wantCaller: ""},
		{name: "no authorization header", mdKey: "x-other", mdValue: "whatever", wantCaller: ""},
		{name: "empty token", mdKey: "authorization", mdValue: "", wantCaller: ""},
		{name: "wrong token", mdKey: "authorization", mdValue: "nope", wantCaller: ""},
		// A prefix of a valid token must not pass: ConstantTimeCompare is
		// length-aware, and a truncating compare would accept it.
		{name: "prefix of a valid token", mdKey: "authorization", mdValue: "dashboard-sec", wantCaller: ""},
		{name: "legacy token, raw", mdKey: "authorization", mdValue: "legacy-secret", wantCaller: "default"},
		{name: "named token, raw", mdKey: "authorization", mdValue: "dashboard-secret", wantCaller: "dashboard"},
		{name: "named token, bearer prefix", mdKey: "authorization", mdValue: "Bearer ci-secret", wantCaller: "ci"},
		{name: "named token, lowercase bearer", mdKey: "authorization", mdValue: "bearer ci-secret", wantCaller: "ci"},
		{name: "bearer metadata key fallback", mdKey: "bearer", mdValue: "dashboard-secret", wantCaller: "dashboard"},
	}

	interceptor := newAuthInterceptor(testTokens)
	info := &grpc.UnaryServerInfo{FullMethod: "/api.Dex/DeleteClient"}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			gotCaller := ""
			handler := func(ctx context.Context, req interface{}) (interface{}, error) {
				called = true
				gotCaller, _ = ctx.Value(callerKey{}).(string)
				return "ok", nil
			}

			_, err := interceptor(ctxWithMD(tc.mdKey, tc.mdValue), nil, info, handler)

			if tc.wantCaller == "" {
				if err == nil {
					t.Fatal("expected the call to be rejected, but it was accepted")
				}
				if status.Code(err) != codes.Unauthenticated {
					t.Errorf("code: got %v, want %v", status.Code(err), codes.Unauthenticated)
				}
				if called {
					t.Error("handler ran for a rejected call")
				}
				return
			}

			if err != nil {
				t.Fatalf("expected the call to be accepted: %v", err)
			}
			if !called {
				t.Fatal("handler did not run for an accepted call")
			}
			if gotCaller != tc.wantCaller {
				t.Errorf("caller: got %q, want %q", gotCaller, tc.wantCaller)
			}
		})
	}
}

func TestAuditInterceptor(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		caller   string
		actor    string
		wantLog  bool
		wantAttr []string
	}{
		{
			name: "read is not audited", method: "/api.Dex/ListClients",
			caller: "dashboard", wantLog: false,
		},
		{
			name: "delete is audited", method: "/api.Dex/DeleteClient",
			caller: "dashboard", actor: "admin@example.com", wantLog: true,
			wantAttr: []string{"method=DeleteClient", "caller=dashboard", "actor=admin@example.com"},
		},
		// Upstream added these after the fork's audit list was written; they end
		// sessions and clear second factors, so they must not slip through.
		{
			name: "terminate is audited", method: "/api.Dex/TerminateSessionsByUser",
			caller: "ci", wantLog: true,
			wantAttr: []string{"method=TerminateSessionsByUser", "caller=ci", "actor=unknown"},
		},
		{
			name: "reset mfa is audited", method: "/api.Dex/ResetMFA",
			caller: "ci", wantLog: true,
			wantAttr: []string{"method=ResetMFA", "caller=ci"},
		},
		{
			name: "no caller when only mutual TLS authenticates", method: "/api.Dex/DeleteClient",
			caller: "", wantLog: true,
			wantAttr: []string{"caller=none"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))

			ctx := context.Background()
			if tc.caller != "" {
				ctx = context.WithValue(ctx, callerKey{}, tc.caller)
			}
			if tc.actor != "" {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(actorHeader, tc.actor))
			}

			handler := func(context.Context, interface{}) (interface{}, error) { return "ok", nil }
			if _, err := newAuditInterceptor(logger)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tc.method}, handler); err != nil {
				t.Fatalf("interceptor returned an error: %v", err)
			}

			got := buf.String()
			if !tc.wantLog {
				if got != "" {
					t.Errorf("expected no audit line for a read, got: %s", got)
				}
				return
			}
			for _, attr := range tc.wantAttr {
				if !strings.Contains(got, attr) {
					t.Errorf("audit line is missing %q; got: %s", attr, got)
				}
			}
		})
	}
}

// A failing call still has to be recorded: an attempt to delete a client that
// was rejected is exactly the kind of thing an audit trail is for.
func TestAuditInterceptorRecordsFailures(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := func(context.Context, interface{}) (interface{}, error) {
		return nil, status.Error(codes.NotFound, "no such client")
	}
	ctx := context.WithValue(context.Background(), callerKey{}, "ci")
	_, err := newAuditInterceptor(logger)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/api.Dex/DeleteClient"}, handler)
	if err == nil {
		t.Fatal("expected the handler's error to be passed through")
	}

	got := buf.String()
	for _, want := range []string{"level=WARN", "method=DeleteClient", "caller=ci"} {
		if !strings.Contains(got, want) {
			t.Errorf("audit line is missing %q; got: %s", want, got)
		}
	}
}

func TestGRPCValidateTokens(t *testing.T) {
	tests := []struct {
		name    string
		grpc    GRPC
		wantErr string
	}{
		{name: "no tokens at all", grpc: GRPC{}},
		{name: "legacy token only", grpc: GRPC{Token: "s"}},
		{
			name: "legacy plus named",
			grpc: GRPC{Token: "s", Tokens: []GRPCToken{{Name: "ci", Token: "t"}}},
		},
		{
			name:    "name is required",
			grpc:    GRPC{Tokens: []GRPCToken{{Token: "t"}}},
			wantErr: "name is required",
		},
		{
			name:    "token is required",
			grpc:    GRPC{Tokens: []GRPCToken{{Name: "ci"}}},
			wantErr: "token is required",
		},
		{
			name:    "duplicate name",
			grpc:    GRPC{Tokens: []GRPCToken{{Name: "ci", Token: "a"}, {Name: "ci", Token: "b"}}},
			wantErr: "duplicate name",
		},
		{
			name:    "two names sharing a secret",
			grpc:    GRPC{Tokens: []GRPCToken{{Name: "ci", Token: "a"}, {Name: "dashboard", Token: "a"}}},
			wantErr: "could not tell them apart",
		},
		{
			name:    "named token colliding with the legacy one",
			grpc:    GRPC{Token: "a", Tokens: []GRPCToken{{Name: "ci", Token: "a"}}},
			wantErr: "could not tell them apart",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.grpc.validateTokens()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the config to be accepted: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected the config to be rejected with %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error: got %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
