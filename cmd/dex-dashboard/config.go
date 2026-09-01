package main

import (
	"fmt"
	"os"
	"time"

	"github.com/ghodss/yaml"
)

// Config is the dashboard's YAML configuration. It is deliberately separate
// from dex's own config: the dashboard is a client of dex, not part of it.
type Config struct {
	// Listen is the address the dashboard serves on. Bind it to a private
	// interface; this panel is not meant to face the internet.
	Listen string `json:"listen"`

	// BaseURL is the dashboard's own public URL, used to build the OIDC
	// redirect URI. Must match the redirect URI registered on the dex client.
	BaseURL string `json:"baseURL"`

	Dex   DexConfig   `json:"dex"`
	OIDC  OIDCConfig  `json:"oidc"`
	Admin AdminConfig `json:"admin"`
}

// DexConfig points at the dex gRPC API the dashboard administers.
type DexConfig struct {
	// GRPCAddress is the host:port of dex's gRPC API.
	GRPCAddress string `json:"grpcAddress"`

	// Token authenticates against the gRPC API. It stays in this process and is
	// never sent to a browser: that is the whole reason the dashboard is a
	// backend and not a single-page app talking to dex directly.
	Token string `json:"token"`

	// TokenFile reads the token from disk instead, so it can come from a secret
	// mount rather than the config file. Takes precedence over Token.
	TokenFile string `json:"tokenFile"`

	// CACert enables TLS against the gRPC API with the given root. Without it
	// the connection is plaintext, which is only acceptable over loopback.
	CACert string `json:"caCert"`

	// TelemetryURL is dex's telemetry endpoint, e.g. http://dex:5558. It backs
	// the Status view. That endpoint has no authentication of its own, so it
	// must be reachable from the dashboard and from nowhere else; the dashboard
	// reads it server-side and never proxies it to a browser.
	TelemetryURL string `json:"telemetryURL"`
}

// OIDCConfig is how administrators log in: against dex itself.
type OIDCConfig struct {
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`

	// Scopes requested at login. "groups" is added automatically because the
	// authorization check below depends on it.
	Scopes []string `json:"scopes"`
}

// AdminConfig decides who gets in, and who may change anything once in.
type AdminConfig struct {
	// Groups lists the groups that grant access. A user needs one of them.
	Groups []string `json:"groups"`

	// Emails grants access to specific addresses. It exists for the
	// chicken-and-egg case: the connector that carries the admin group may be
	// the one that is broken, and someone still has to get in and fix it.
	Emails []string `json:"emails"`

	// WriteGroups and WriteEmails grant permission to change things. Read
	// access is not enough: a panel that lets everyone who can look also delete
	// is one careless click from an outage, and plenty of people need to read
	// dex's state without needing to edit it.
	//
	// Leaving both empty makes the panel read-only for everybody, which is the
	// safe default for a console that administers the identity provider.
	WriteGroups []string `json:"writeGroups"`
	WriteEmails []string `json:"writeEmails"`

	// SessionTTL bounds a dashboard session. Defaults to 8h.
	SessionTTL time.Duration `json:"-"`
	// SessionTTLRaw is the YAML form, e.g. "8h".
	SessionTTLRaw string `json:"sessionTTL"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if c.Dex.TokenFile != "" {
		token, err := os.ReadFile(c.Dex.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("read dex.tokenFile: %w", err)
		}
		c.Dex.Token = string(trimSpace(token))
	}

	if c.Admin.SessionTTLRaw != "" {
		d, err := time.ParseDuration(c.Admin.SessionTTLRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid admin.sessionTTL %q: %w", c.Admin.SessionTTLRaw, err)
		}
		c.Admin.SessionTTL = d
	}
	if c.Admin.SessionTTL <= 0 {
		c.Admin.SessionTTL = 8 * time.Hour
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:5556"
	}

	return &c, c.validate()
}

// validate refuses a configuration that would start an admin panel nobody can
// administer, or one that anybody can walk into.
func (c *Config) validate() error {
	switch {
	case c.BaseURL == "":
		return fmt.Errorf("baseURL is required: it builds the OIDC redirect URI")
	case c.Dex.GRPCAddress == "":
		return fmt.Errorf("dex.grpcAddress is required")
	case c.OIDC.Issuer == "":
		return fmt.Errorf("oidc.issuer is required")
	case c.OIDC.ClientID == "":
		return fmt.Errorf("oidc.clientID is required")
	case len(c.Admin.Groups) == 0 && len(c.Admin.Emails) == 0:
		// Without this the gate would admit every user dex can authenticate,
		// which for an administration panel is the same as no gate at all.
		return fmt.Errorf("admin.groups or admin.emails is required: refusing to grant every authenticated user administrative access")
	}
	return nil
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
