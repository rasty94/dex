package keystone

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestTokenIdentity_UsesSelfValidation(t *testing.T) {
	// adminToken := "admin-token-123" // Not used in self-validation
	subjectToken := "user-token-abc"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/v3/auth/tokens", "/v3/auth/tokens/":
			if r.Method == "GET" {
				// 1. Validate Token (Self-Validation)
				// Verify X-Auth-Token is the SUBJECT token (not admin token)
				authToken := r.Header.Get("X-Auth-Token")
				if authToken != subjectToken {
					t.Errorf("Validation Request: X-Auth-Token = %q, want %q (Subject Token)", authToken, subjectToken)
				}
				// Verify X-Subject-Token is the USER token
				subjToken := r.Header.Get("X-Subject-Token")
				if subjToken != subjectToken {
					t.Errorf("Validation Request: X-Subject-Token = %q, want %q (Subject Token)", subjToken, subjectToken)
				}

				// Return valid response
				json.NewEncoder(w).Encode(map[string]interface{}{
					"token": map[string]interface{}{
						"user": map[string]interface{}{"id": "user-id", "name": "user"},
					},
				})
				return
			}
		case "/v3/users/user-id":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"user": map[string]interface{}{"id": "user-id", "name": "user", "email": "user@example.com"},
			})
			return
		case "/v3/users/user-id/groups":
			json.NewEncoder(w).Encode(map[string]interface{}{"groups": []interface{}{}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := Config{
		Host:          ts.URL,
		Domain:        "default",
		AdminUsername: "admin",
		AdminPassword: "password",
	}

	connectorInstance, err := c.Open("test", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	kConn := connectorInstance.(*conn)

	_, err = kConn.TokenIdentity(context.Background(), "token", subjectToken)
	if err != nil {
		t.Fatalf("TokenIdentity failed: %v", err)
	}
}
