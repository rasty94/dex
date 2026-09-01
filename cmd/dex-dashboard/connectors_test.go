package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	config := []byte(`{
		"host": "ldap.example.com",
		"bindDN": "cn=admin,dc=example,dc=com",
		"bindPW": "el-secreto",
		"clientSecret": "otro-secreto",
		"insecureNoSSL": true,
		"userSearch": {"baseDN": "ou=people", "password": "anidado"},
		"emptyPassword": ""
	}`)

	out, err := redactSecrets(config)
	if err != nil {
		t.Fatalf("redactSecrets: %v", err)
	}

	for _, leaked := range []string{"el-secreto", "otro-secreto", "anidado"} {
		if strings.Contains(out, leaked) {
			t.Errorf("secret %q was rendered into the page", leaked)
		}
	}
	// Everything that is not a secret must survive, including nested values and
	// non-string fields.
	for _, kept := range []string{"ldap.example.com", "cn=admin", "ou=people", "true"} {
		if !strings.Contains(out, kept) {
			t.Errorf("non-secret %q was lost", kept)
		}
	}
	// An empty secret is not worth marking as "unchanged": there is nothing to keep.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}
	if parsed["emptyPassword"] != "" {
		t.Errorf("empty secret became %q, want it left empty", parsed["emptyPassword"])
	}
}

func TestRestoreSecrets(t *testing.T) {
	stored := []byte(`{"host":"ldap.example.com","bindPW":"el-secreto","userSearch":{"password":"anidado"}}`)

	t.Run("markers are restored from what is stored", func(t *testing.T) {
		submitted := []byte(`{"host":"ldap.example.com","bindPW":"__unchanged__","userSearch":{"password":"__unchanged__"}}`)
		merged, err := restoreSecrets(submitted, stored)
		if err != nil {
			t.Fatalf("restoreSecrets: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(merged, &got); err != nil {
			t.Fatalf("merged config is not valid JSON: %v", err)
		}
		if got["bindPW"] != "el-secreto" {
			t.Errorf("bindPW = %v, want the stored secret back", got["bindPW"])
		}
		nested := got["userSearch"].(map[string]any)
		if nested["password"] != "anidado" {
			t.Errorf("nested password = %v, want the stored secret back", nested["password"])
		}
	})

	t.Run("a typed value replaces the stored one", func(t *testing.T) {
		submitted := []byte(`{"host":"ldap.example.com","bindPW":"rotado"}`)
		merged, err := restoreSecrets(submitted, stored)
		if err != nil {
			t.Fatalf("restoreSecrets: %v", err)
		}
		var got map[string]any
		json.Unmarshal(merged, &got)
		if got["bindPW"] != "rotado" {
			t.Errorf("bindPW = %v, want the newly typed secret", got["bindPW"])
		}
	})

	t.Run("a marker with nothing behind it is dropped", func(t *testing.T) {
		submitted := []byte(`{"host":"x","newSecret":"__unchanged__"}`)
		merged, err := restoreSecrets(submitted, []byte(`{"host":"x"}`))
		if err != nil {
			t.Fatalf("restoreSecrets: %v", err)
		}
		if strings.Contains(string(merged), unchangedMarker) {
			t.Errorf("the literal marker was written into the config: %s", merged)
		}
	})

	t.Run("invalid JSON is refused", func(t *testing.T) {
		if _, err := restoreSecrets([]byte(`{nope`), stored); err == nil {
			t.Error("expected an error for malformed JSON")
		}
	})
}

// A round trip must not change a config the operator did not touch.
func TestRedactRestoreRoundTrip(t *testing.T) {
	stored := []byte(`{"host":"ldap.example.com","bindPW":"el-secreto","insecureNoSSL":true}`)

	shown, err := redactSecrets(stored)
	if err != nil {
		t.Fatalf("redactSecrets: %v", err)
	}
	merged, err := restoreSecrets([]byte(shown), stored)
	if err != nil {
		t.Fatalf("restoreSecrets: %v", err)
	}

	var before, after map[string]any
	json.Unmarshal(stored, &before)
	json.Unmarshal(merged, &after)
	for k, want := range before {
		if got := after[k]; got != want {
			t.Errorf("%s changed on a round trip: %v -> %v", k, want, got)
		}
	}
}

// dex only checks that a connector config is valid JSON, so a typo is stored
// and then breaks every login through that connector. The dashboard decodes
// into the real config type to catch it first.
func TestValidateConnectorConfig(t *testing.T) {
	tests := []struct {
		name     string
		connType string
		config   string
		wantErr  string
	}{
		{
			name:     "a good config passes",
			connType: "mockCallback",
			config:   `{}`,
		},
		{
			name:     "unknown connector type",
			connType: "noexiste",
			config:   `{}`,
			wantErr:  "unknown connector type",
		},
		{
			// Go matches field names case-insensitively, so "clientId" for
			// "clientID" is accepted here exactly as dex accepts it in YAML.
			// That is not a typo the dashboard should invent an error for.
			name:     "a case difference is not an error",
			connType: "oidc",
			config:   `{"issuer":"https://x","clientId":"a","clientSecret":"b","redirectURI":"https://y"}`,
		},
		{
			name:     "a field that does not exist is caught",
			connType: "oidc",
			config:   `{"issuer":"https://x","clientSecrets":"b"}`,
			wantErr:  "not a valid oidc configuration",
		},
		{
			name:     "a wrongly typed field is caught",
			connType: "oidc",
			config:   `{"issuer": 42}`,
			wantErr:  "not a valid oidc configuration",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConnectorConfig(tc.connType, []byte(tc.config))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
