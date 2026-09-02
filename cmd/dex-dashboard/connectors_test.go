package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dexidp/dex/server/tokens"
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

// The sessions view builds a subject from a user id and a connector id. If that
// encoding ever drifts from the one dex puts in the "sub" claim, the lookup
// silently returns nothing for every user, so it is pinned here against dex's
// own encoder rather than against a hardcoded string.
func TestSubjectEncodingMatchesDex(t *testing.T) {
	got, err := tokens.GenSubject("08a8684b-db88-4b73-90a9-3cd1661f5466", "local")
	if err != nil {
		t.Fatalf("GenSubject: %v", err)
	}
	if got == "" {
		t.Fatal("GenSubject returned an empty subject")
	}

	// The same pair must always encode the same way, and a different connector
	// must not collide with it: both halves have to reach the wire.
	same, _ := tokens.GenSubject("08a8684b-db88-4b73-90a9-3cd1661f5466", "local")
	if same != got {
		t.Errorf("encoding is not stable: %q then %q", got, same)
	}
	other, _ := tokens.GenSubject("08a8684b-db88-4b73-90a9-3cd1661f5466", "keystone")
	if other == got {
		t.Error("subjects for different connectors collide")
	}
}

// The skeleton exists so nobody has to write connector JSON against a schema
// they cannot see. It has to be valid for the type it describes, and it has to
// lead with the fields that matter.
func TestConnectorSkeleton(t *testing.T) {
	t.Run("every known type produces a valid skeleton", func(t *testing.T) {
		for _, ct := range ConnectorTypes() {
			skeleton, err := ConnectorSkeleton(ct)
			if err != nil {
				t.Errorf("%s: %v", ct, err)
				continue
			}
			// A skeleton the panel would refuse to save is worse than none.
			if err := validateConnectorConfig(ct, []byte(skeleton)); err != nil {
				t.Errorf("%s: its own skeleton fails validation: %v", ct, err)
			}
		}
	})

	t.Run("fields keep struct order, not alphabetical", func(t *testing.T) {
		skeleton, err := ConnectorSkeleton("oidc")
		if err != nil {
			t.Fatalf("ConnectorSkeleton: %v", err)
		}
		issuer := strings.Index(skeleton, `"issuer"`)
		clientID := strings.Index(skeleton, `"clientID"`)
		// "basicAuthUnsupported" sorts before both alphabetically; if it comes
		// first, the ordering was lost and the essentials are buried.
		tuning := strings.Index(skeleton, `"basicAuthUnsupported"`)
		if issuer < 0 || clientID < 0 || tuning < 0 {
			t.Fatalf("skeleton is missing expected fields:\n%s", skeleton)
		}
		if !(issuer < clientID && clientID < tuning) {
			t.Errorf("fields are not in struct order:\n%s", skeleton)
		}
	})

	t.Run("unknown type is refused", func(t *testing.T) {
		if _, err := ConnectorSkeleton("noexiste"); err == nil {
			t.Error("expected an error for an unknown connector type")
		}
	})
}

// A sub pasted from an ID token has to come apart into the pair it encodes, or
// the browser-session lookup — which is keyed on (userID, connectorID) and not
// on the subject — cannot run at all for that half of the form.
func TestSubjectRoundTrips(t *testing.T) {
	const (
		wantUser = "08a8684b-db88-4b73-90a9-3cd1661f5466"
		wantConn = "keystone"
	)

	sub, err := tokens.GenSubject(wantUser, wantConn)
	if err != nil {
		t.Fatalf("GenSubject: %v", err)
	}

	gotUser, gotConn, err := tokens.ParseSubject(sub)
	if err != nil {
		t.Fatalf("ParseSubject: %v", err)
	}
	if gotUser != wantUser || gotConn != wantConn {
		t.Errorf("round trip: got (%q, %q), want (%q, %q)", gotUser, gotConn, wantUser, wantConn)
	}

	// Garbage must be reported, not silently decoded into an empty pair: the
	// caller falls back to the refresh-token lookup on an error, and would list
	// the sessions of "" on the "" connector if this returned nil.
	if _, _, err := tokens.ParseSubject("not-a-subject"); err == nil {
		t.Error("expected an error for a subject that is not valid wire format")
	}
}
