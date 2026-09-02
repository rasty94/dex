package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/dexidp/dex/storage"
)

// dex's own MFA runs after the identity exists; Keystone's runs inside the
// credential exchange. Pointing an authenticator at a Keystone connector asks
// the same user for two second factors in a row, and the configuration that
// does it is the one where nobody wrote anything: an empty connectorTypes means
// every connector.
func TestWarnOnDoubleSecondFactor(t *testing.T) {
	keystone := []storage.Connector{{ID: "openstack", Type: "keystone"}}
	noKeystone := []storage.Connector{{ID: "corp", Type: "ldap"}}

	tests := []struct {
		name       string
		auth       MFAAuthenticator
		connectors []storage.Connector
		wantWarn   bool
	}{
		{
			name:       "empty connectorTypes reaches keystone",
			auth:       MFAAuthenticator{ID: "totp", Type: "TOTP"},
			connectors: keystone,
			wantWarn:   true,
		},
		{
			name:       "keystone listed explicitly",
			auth:       MFAAuthenticator{ID: "totp", Type: "TOTP", ConnectorTypes: []string{"ldap", "keystone"}},
			connectors: keystone,
			wantWarn:   true,
		},
		{
			name:       "keystone left out is the configuration we ask for",
			auth:       MFAAuthenticator{ID: "totp", Type: "TOTP", ConnectorTypes: []string{"ldap", "local"}},
			connectors: keystone,
			wantWarn:   false,
		},
		{
			// No point warning about a connector this deployment does not have.
			name:       "no keystone connector configured",
			auth:       MFAAuthenticator{ID: "totp", Type: "TOTP"},
			connectors: noKeystone,
			wantWarn:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			warnOnDoubleSecondFactor([]MFAAuthenticator{tc.auth}, tc.connectors, logger)

			warned := strings.Contains(buf.String(), "two in a row")
			if warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v; log was: %s", warned, tc.wantWarn, buf.String())
			}
			if tc.wantWarn && !strings.Contains(buf.String(), "connectorTypes") {
				t.Error("the warning should say how to fix it")
			}
		})
	}
}
