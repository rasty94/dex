package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dexidp/dex/connector"
)

// mockKeystoneServer builds a test HTTP server that simulates Keystone v3.
// The returned mux can be extended per test case.
func mockKeystoneServer(t *testing.T) (*httptest.Server, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, mux
}

// newTestConn returns a conn pointing at the given test server.
func newTestConn(host string) *conn {
	return &conn{
		Host:          host,
		AdminUsername: "admin",
		AdminPassword: "admin-pass",
		Domain:        domainKeystone{ID: "default"},
		client:        http.DefaultClient,
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
}

// ─────────────────────────────────────────────
// Helpers: standard JSON responses
// ─────────────────────────────────────────────

func writeToken(w http.ResponseWriter, userID, userName, userToken string) {
	w.Header().Set("X-Subject-Token", userToken)
	w.WriteHeader(http.StatusCreated)
	resp := tokenResponse{
		Token: token{
			User: userKeystone{
				ID:   userID,
				Name: userName,
			},
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func writeUser(w http.ResponseWriter, name, email, id string) {
	w.WriteHeader(http.StatusOK)
	resp := userResponse{}
	resp.User.Name = name
	resp.User.Email = email
	resp.User.ID = id
	json.NewEncoder(w).Encode(resp)
}

func writeGroups(w http.ResponseWriter, groups ...string) {
	w.WriteHeader(http.StatusOK)
	gs := make([]group, len(groups))
	for i, g := range groups {
		gs[i] = group{ID: fmt.Sprintf("id-%d", i), Name: g}
	}
	json.NewEncoder(w).Encode(groupsResponse{Groups: gs})
}

// ─────────────────────────────────────────────
// Tests: Login — standard flow
// ─────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "user-42", "jdoe", "tok-abc")
	})
	mux.HandleFunc("/v3/users/user-42", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "user-42")
	})
	mux.HandleFunc("/v3/users/user-42/groups", func(w http.ResponseWriter, r *http.Request) {
		writeGroups(w, "admins", "developers")
	})

	identity, valid, err := c.Login(context.Background(), connector.Scopes{Groups: true}, "jdoe", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !valid {
		t.Fatal("expected valid=true")
	}
	if identity.UserID != "user-42" {
		t.Errorf("UserID: got %q, want %q", identity.UserID, "user-42")
	}
	if identity.Email != "jdoe@example.com" {
		t.Errorf("Email: got %q, want %q", identity.Email, "jdoe@example.com")
	}
	if len(identity.Groups) != 2 {
		t.Errorf("Groups: got %v, want 2 entries", identity.Groups)
	}
}

func TestLogin_InvalidPassword(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, valid, err := c.Login(context.Background(), connector.Scopes{}, "jdoe", "wrong")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Fatal("expected valid=false for bad credentials")
	}
}

// ─────────────────────────────────────────────
// Tests: Login — TOTP/MFA flow
// ─────────────────────────────────────────────

func TestLogin_TOTPRequired(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	// Step 1: Keystone returns 401 + receipt → ErrTOTPRequired
	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("openstack-auth-receipt", "receipt-xyz")
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, _, err := c.Login(context.Background(), connector.Scopes{}, "jdoe", "pass")
	if err == nil {
		t.Fatal("expected ErrTOTPRequired, got nil")
	}
	totpErr, ok := err.(ErrTOTPRequired)
	if !ok {
		t.Fatalf("expected ErrTOTPRequired, got %T: %v", err, err)
	}
	if totpErr.Receipt != "receipt-xyz" {
		t.Errorf("Receipt: got %q, want %q", totpErr.Receipt, "receipt-xyz")
	}
}

func TestLogin_TOTPSuccessWithReceipt(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	callCount := 0
	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// Verify receipt header is forwarded
		if r.Header.Get("openstack-auth-receipt") == "" {
			t.Error("expected openstack-auth-receipt header in TOTP step")
		}
		writeToken(w, "user-42", "jdoe", "tok-totp")
	})
	mux.HandleFunc("/v3/users/user-42", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "user-42")
	})
	mux.HandleFunc("/v3/users/user-42/groups", func(w http.ResponseWriter, r *http.Request) {
		writeGroups(w, "users")
	})

	ctx := context.WithValue(context.Background(), TOTPContextKey, "123456")
	ctx = context.WithValue(ctx, ReceiptContextKey, "receipt-xyz")

	identity, valid, err := c.Login(ctx, connector.Scopes{Groups: true}, "jdoe", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !valid {
		t.Fatal("expected valid=true after TOTP")
	}
	if identity.Email != "jdoe@example.com" {
		t.Errorf("Email: got %q", identity.Email)
	}
}

func TestLogin_InvalidTOTP(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		// Wrong TOTP → 401 without new receipt means invalid code
		w.WriteHeader(http.StatusUnauthorized)
	})

	ctx := context.WithValue(context.Background(), TOTPContextKey, "000000")
	ctx = context.WithValue(ctx, ReceiptContextKey, "receipt-xyz")

	_, valid, err := c.Login(ctx, connector.Scopes{}, "jdoe", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Fatal("expected valid=false for invalid TOTP")
	}
}

// ─────────────────────────────────────────────
// Tests: Login — UserIDKey derivation
// ─────────────────────────────────────────────

func TestLogin_UserIDKey_Email(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)
	c.UserIDKey = "email"

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "native-id", "jdoe", "tok-abc")
	})
	mux.HandleFunc("/v3/users/native-id", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "native-id")
	})
	mux.HandleFunc("/v3/users/native-id/groups", func(w http.ResponseWriter, r *http.Request) {
		writeGroups(w)
	})

	identity, valid, err := c.Login(context.Background(), connector.Scopes{}, "jdoe", "pass")
	if err != nil || !valid {
		t.Fatalf("Login failed: valid=%v err=%v", valid, err)
	}
	// UserID must be a UUID derived from email, not the native Keystone ID
	if identity.UserID == "native-id" {
		t.Error("UserID should be SHA1-UUID of email, not native Keystone ID")
	}
	if identity.UserID == "" {
		t.Error("UserID should not be empty")
	}
}

// ─────────────────────────────────────────────
// Tests: TokenIdentity
// ─────────────────────────────────────────────

func TestTokenIdentity_Success(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("X-Subject-Token") == "" {
			t.Error("expected X-Subject-Token header")
		}
		w.WriteHeader(http.StatusOK)
		resp := tokenResponse{
			Token: token{User: userKeystone{ID: "user-99", Name: "alice"}},
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/v3/users/user-99", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "alice", "alice@example.com", "user-99")
	})
	mux.HandleFunc("/v3/users/user-99/groups", func(w http.ResponseWriter, r *http.Request) {
		writeGroups(w, "ops")
	})

	identity, err := c.TokenIdentity(context.Background(), "urn:ietf:params:oauth:token-type:access_token", "ks-token-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.UserID != "user-99" {
		t.Errorf("UserID: got %q, want %q", identity.UserID, "user-99")
	}
	if identity.Email != "alice@example.com" {
		t.Errorf("Email: got %q", identity.Email)
	}
}

func TestTokenIdentity_InvalidToken(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintln(w, `{"error": {"message": "The token is invalid"}}`)
	})

	_, err := c.TokenIdentity(context.Background(), "", "bad-token")
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
}

// ─────────────────────────────────────────────
// Tests: Refresh
// ─────────────────────────────────────────────

func TestRefresh_UserExists(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	// Admin auth
	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "admin-id", "admin", "admin-tok")
	})
	mux.HandleFunc("/v3/users/user-42", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "user-42")
	})
	mux.HandleFunc("/v3/users/user-42/groups", func(w http.ResponseWriter, r *http.Request) {
		writeGroups(w, "devs")
	})

	existing := connector.Identity{UserID: "user-42", Username: "jdoe"}
	refreshed, err := c.Refresh(context.Background(), connector.Scopes{Groups: true}, existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refreshed.Groups) == 0 {
		t.Error("expected groups to be refreshed")
	}
}

func TestRefresh_UserDeleted(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "admin-id", "admin", "admin-tok")
	})
	// User not found
	mux.HandleFunc("/v3/users/deleted-user", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	existing := connector.Identity{UserID: "deleted-user"}
	_, err := c.Refresh(context.Background(), connector.Scopes{}, existing)
	if err == nil {
		t.Fatal("expected error when user is deleted")
	}
}
