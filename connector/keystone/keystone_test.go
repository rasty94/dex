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

// The receipt is Keystone's own record that the password was already accepted,
// so the second step must not resend it. This is what lets the login form stop
// carrying the plaintext password through the TOTP step.
func TestLogin_ReceiptStepOmitsPassword(t *testing.T) {
	tests := []struct {
		name            string
		receipt         string
		wantMethods     []string
		wantPasswordKey bool
	}{
		{
			name:            "with receipt, only the missing method travels",
			receipt:         "receipt-xyz",
			wantMethods:     []string{"totp"},
			wantPasswordKey: false,
		},
		{
			name:            "without receipt, both methods travel in one request",
			receipt:         "",
			wantMethods:     []string{"password", "totp"},
			wantPasswordKey: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, mux := mockKeystoneServer(t)
			c := newTestConn(srv.URL)

			var identityBody map[string]any
			mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Auth struct {
						Identity map[string]any `json:"identity"`
					} `json:"auth"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decoding request body: %v", err)
				}
				identityBody = body.Auth.Identity
				writeToken(w, "user-42", "jdoe", "tok-totp")
			})
			mux.HandleFunc("/v3/users/user-42", func(w http.ResponseWriter, r *http.Request) {
				writeUser(w, "jdoe", "jdoe@example.com", "user-42")
			})

			ctx := context.WithValue(context.Background(), TOTPContextKey, "123456")
			if tc.receipt != "" {
				ctx = context.WithValue(ctx, ReceiptContextKey, tc.receipt)
			}

			if _, _, err := c.Login(ctx, connector.Scopes{}, "jdoe", "hunter2"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			methods, _ := identityBody["methods"].([]any)
			got := make([]string, 0, len(methods))
			for _, m := range methods {
				got = append(got, fmt.Sprintf("%v", m))
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.wantMethods) {
				t.Errorf("methods: got %v, want %v", got, tc.wantMethods)
			}

			if _, present := identityBody["password"]; present != tc.wantPasswordKey {
				t.Errorf("password block present = %v, want %v", present, tc.wantPasswordKey)
			}
		})
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

// TestRefresh_UsesConnectorDataUserID checks that Refresh addresses the
// Keystone API with the id stashed in ConnectorData rather than Identity.UserID.
// With UserIDKey set to email or username the two differ: UserID is a
// synthetic UUID derived from that field and would make every /v3/users/<id>
// call return 404, breaking the refresh grant.
func TestRefresh_UsesConnectorDataUserID(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	const (
		keystoneID   = "user-42"
		syntheticUID = "6f1a2b3c-4d5e-5f60-8a9b-0c1d2e3f4a5b"
	)

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "admin-id", "admin", "admin-tok")
	})
	mux.HandleFunc("/v3/users/user-42", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", keystoneID)
	})
	mux.HandleFunc("/v3/users/user-42/groups", func(w http.ResponseWriter, r *http.Request) {
		writeGroups(w, "devs")
	})
	// If Refresh addressed the synthetic UUID instead, this handler would fire
	// a 404 by falling through to ServeMux's default NotFound response.

	existing := connector.Identity{UserID: syntheticUID, ConnectorData: []byte(keystoneID)}
	refreshed, err := c.Refresh(context.Background(), connector.Scopes{Groups: true}, existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refreshed.Groups) == 0 {
		t.Error("expected groups to be refreshed")
	}
	// The identity keeps its own UserID: only the API calls are remapped.
	if refreshed.UserID != syntheticUID {
		t.Errorf("Refresh changed UserID to %q, want %q", refreshed.UserID, syntheticUID)
	}
}

// TestRefresh_FallsBackToUserID covers offline sessions stored before
// ConnectorData carried the Keystone user id, where UserID held it directly.
func TestRefresh_FallsBackToUserID(t *testing.T) {
	srv, mux := mockKeystoneServer(t)
	c := newTestConn(srv.URL)

	mux.HandleFunc("/v3/auth/tokens/", func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "admin-id", "admin", "admin-tok")
	})
	mux.HandleFunc("/v3/users/user-42", func(w http.ResponseWriter, r *http.Request) {
		writeUser(w, "jdoe", "jdoe@example.com", "user-42")
	})

	existing := connector.Identity{UserID: "user-42"}
	if _, err := c.Refresh(context.Background(), connector.Scopes{}, existing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
