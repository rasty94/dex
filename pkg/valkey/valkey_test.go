package valkey

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// An empty address is how an operator says "keep everything in memory". It has
// to be a nil client and not an error, because every caller treats nil as
// "there is no shared store" and falls back to its local implementation.
func TestNoAddressMeansNoClient(t *testing.T) {
	c, err := New(t.Context(), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c != nil {
		t.Fatal("an empty address must not open a connection")
	}
}

func TestNewPingsAndPrefixes(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if got := c.Key("rl:abc"); got != "dex:rl:abc" {
		t.Errorf("Key = %q, want dex:rl:abc", got)
	}
}

// The key must not carry the secret it identifies: a Valkey shared with other
// services would otherwise list live tokens and usernames as key names.
func TestHashKeyHidesTheSecret(t *testing.T) {
	m := miniredis.RunT(t)
	c, err := New(t.Context(), Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	const secret = "gAAAAABlive-keystone-token"
	got := c.HashKey("tok", secret)

	if strings.Contains(got, secret) {
		t.Errorf("the key carries the secret verbatim: %q", got)
	}
	if !strings.HasPrefix(got, "dex:tok:") {
		t.Errorf("HashKey = %q, want the dex:tok: prefix", got)
	}
	if got != c.HashKey("tok", secret) {
		t.Error("HashKey is not stable, so a lookup could never hit")
	}
}

// A configured address that does not answer is a startup error, not a silent
// fallback: an operator who asked for a shared store must not discover during
// an incident that it was never shared.
func TestUnreachableAddressFailsToStart(t *testing.T) {
	m := miniredis.RunT(t)
	addr := m.Addr()
	m.Close()

	if _, err := New(context.Background(), Config{Addresses: []string{addr}}); err == nil {
		t.Fatal("an unreachable address must fail to start")
	}
}

// Every key dex writes here has an expiry, so volatile-* is no safer than
// allkeys-*: both target exactly what dex stores. The counters of the login
// limiter are the ones that matter -- evicting them hands out a fresh budget.
func TestEvictionRisk(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]string
		want bool
	}{
		{"no limit, so nothing is ever evicted", map[string]string{"maxmemory": "0", "maxmemory-policy": "allkeys-lru"}, false},
		{"limit with noeviction refuses writes instead", map[string]string{"maxmemory": "1073741824", "maxmemory-policy": "noeviction"}, false},
		{"allkeys-lru drops any key", map[string]string{"maxmemory": "1073741824", "maxmemory-policy": "allkeys-lru"}, true},
		{"volatile-ttl targets the keys with an expiry, which is all of dex's", map[string]string{"maxmemory": "1073741824", "maxmemory-policy": "volatile-ttl"}, true},
		{"a server that answered nothing useful is not accused", map[string]string{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := evictionRisk(tc.cfg); got != tc.want {
				t.Errorf("evictionRisk(%v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

// New refuses a configuration that Validate rejects, rather than connecting and
// misbehaving later. miniredis is a real server, so this proves the check runs
// before the dial and not after it.
func TestNewValidatesBeforeConnecting(t *testing.T) {
	m := miniredis.RunT(t)

	_, err := New(t.Context(), Config{Mode: ModeCluster, Addresses: []string{m.Addr()}, DB: 1})
	if err == nil {
		t.Fatal("New accepted a cluster on db 1")
	}
	if !strings.Contains(err.Error(), "database but 0") {
		t.Errorf("New() = %v, want the error to explain the database restriction", err)
	}
}

// A single address with no mode is what every existing deployment has, and it
// has to keep working exactly as before.
func TestStandaloneIsStillTheDefault(t *testing.T) {
	m := miniredis.RunT(t)

	c, err := New(t.Context(), Config{Addresses: []string{m.Addr()}, KeyPrefix: "dex:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if err := c.Do(t.Context(), c.B().Set().Key(c.Key("k")).Value("v").Build()).Error(); err != nil {
		t.Errorf("set against a standalone server: %v", err)
	}
}

// TestTLSConfigCarriesTheClientCertificate proves the configured pair reaches
// the tls.Config the client dials with. Without it the server side can require
// client certificates (tls-auth-clients yes) and dex would present none.
func TestTLSConfigCarriesTheClientCertificate(t *testing.T) {
	certPath, keyPath := writeSelfSignedPair(t)

	tlsCfg, err := tlsConfigFor(TLSConfig{CACert: certPath, Cert: certPath, Key: keyPath})
	if err != nil {
		t.Fatalf("tlsConfigFor() = %v, want no error", err)
	}
	if got := len(tlsCfg.Certificates); got != 1 {
		t.Fatalf("tls.Config carries %d certificates, want 1", got)
	}
	if tlsCfg.RootCAs == nil {
		t.Error("tls.Config carries no root CAs, so the server would not be verified")
	}
}

// TestTLSWithoutAnythingConfiguredStaysPlaintext keeps the default: a
// deployment that configures no TLS at all dials in the clear, as before.
func TestTLSWithoutAnythingConfiguredStaysPlaintext(t *testing.T) {
	tlsCfg, err := tlsConfigFor(TLSConfig{})
	if err != nil {
		t.Fatalf("tlsConfigFor() = %v, want no error", err)
	}
	if tlsCfg != nil {
		t.Errorf("tlsConfigFor() = %v, want nil so the connection stays plaintext", tlsCfg)
	}
}

// TestAnUnreadableClientCertificateNamesTheFile: the pair is read at startup,
// so a wrong path must stop dex there and say which file it could not read,
// rather than fail every handshake later with a server-side error.
func TestAnUnreadableClientCertificateNamesTheFile(t *testing.T) {
	_, err := tlsConfigFor(TLSConfig{Cert: "/nonexistent/dex.crt", Key: "/nonexistent/dex.key"})
	if err == nil {
		t.Fatal("tlsConfigFor() = nil, want an error naming the file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/dex.crt") {
		t.Errorf("tlsConfigFor() = %v, want it to name the certificate file", err)
	}
}

// writeSelfSignedPair leaves a certificate and its key in a temporary directory
// and returns both paths.
func writeSelfSignedPair(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dex-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "dex.crt")
	keyPath = filepath.Join(dir, "dex.key")
	write := func(path, blockType string, der []byte) {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		defer f.Close()
		if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(certPath, "CERTIFICATE", der)
	write(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return certPath, keyPath
}
