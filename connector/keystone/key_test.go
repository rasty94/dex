package keystone

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestTokenIdentity_UserIDKey(t *testing.T) {
	// Mock Keystone Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/v3/auth/tokens", "/v3/auth/tokens/":
			if r.Method == "POST" {
				// Admin login response
				w.Header().Set("X-Subject-Token", "admin-token-123")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"token": map[string]interface{}{
						"user": map[string]interface{}{
							"id":   "admin-id",
							"name": "admin",
						},
					},
				})
				return
			}
			if r.Method == "GET" {
				// Validate token response
				json.NewEncoder(w).Encode(map[string]interface{}{
					"token": map[string]interface{}{
						"user": map[string]interface{}{
							"id":   "user-id-123",
							"name": "testuser",
						},
					},
				})
				return
			}
		case "/v3/users/user-id-123":
			// User details response
			json.NewEncoder(w).Encode(map[string]interface{}{
				"user": map[string]interface{}{
					"id":    "user-id-123",
					"name":  "testuser",
					"email": "test@example.com",
				},
			})
			return
		case "/v3/users/user-id-123/groups":
			json.NewEncoder(w).Encode(map[string]interface{}{"groups": []interface{}{}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	tests := []struct {
		name       string
		userIDKey  string
		wantUserID string
	}{
		{
			name:       "default (no override)",
			userIDKey:  "",
			wantUserID: "user-id-123",
		},
		{
			name:      "override with email",
			userIDKey: "email",
			// uuid.NewSHA1(uuid.NameSpaceURL, []byte("test@example.com"))
			wantUserID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("test@example.com")).String(),
		},
		{
			name:      "override with username",
			userIDKey: "username",
			// uuid.NewSHA1(uuid.NameSpaceURL, []byte("testuser"))
			wantUserID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("testuser")).String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{
				Host:          ts.URL,
				Domain:        "default",
				AdminUsername: "admin",
				AdminPassword: "password",
				UserIDKey:     tt.userIDKey,
			}

			connectorInstance, err := c.Open("test-connector", logger)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}

			// We need to cast to *conn to access internal fields or methods if necessary,
			// but TokenIdentity is part of the interface (mostly).
			// Wait, TokenIdentity is NOT part of connector.Connector interface unless it implements other interfaces.
			// But 'conn' implements it.

			// We can assert the interface to the struct to call TokenIdentity directly if it's not exposed via interface used in tests usually.
			// Actually TokenIdentity is not in connector.Connector. It's used by the callback handler if the connector supports it.
			// This connector DOES NOT satisfy connector.CallbackConnector?
			// Checking keystone.go... it implements PasswordConnector and RefreshConnector.
			// It seems TokenIdentity might be a method intended for a different flow or I missed something?
			// Ah, `TokenIdentity` is likely used when this connector is used as a specific backend?
			// Wait, looking at current code `func (p *conn) TokenIdentity` exists.
			// Let's call it directly.

			kConn := connectorInstance.(*conn)

			identity, err := kConn.TokenIdentity(context.Background(), "token", "some-subject-token")
			if err != nil {
				t.Fatalf("TokenIdentity() error = %v", err)
			}

			if identity.UserID != tt.wantUserID {
				t.Errorf("TokenIdentity() UserID = %v, want %v", identity.UserID, tt.wantUserID)
			}
		})
	}
}
