// Command dex-dashboard is a read-only administration panel for a dex server.
//
// It is deliberately a separate binary from dex: dex is the identity provider,
// and a bug in a management UI should not be a bug in the IdP. The dashboard
// talks to dex over the gRPC API, holds the admin token server-side, and
// authenticates its own users against dex itself over OIDC.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dex-dashboard: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to the dashboard config file")
	flag.Parse()

	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	c, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	dex, err := newDexClient(c.Dex)
	if err != nil {
		return err
	}
	defer dex.Close()

	// Discovery has to reach dex, which may still be starting; a short retry
	// keeps the two services from having to be ordered at deploy time.
	ctx := context.Background()
	auth, err := newAuthenticatorWithRetry(ctx, c, logger)
	if err != nil {
		return err
	}

	d, err := newDashboard(dex, auth, logger)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", auth.handleCallback)
	mux.HandleFunc("/logout", auth.requireAdmin(auth.requireCSRF(auth.handleLogout)))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", auth.requireAdmin(d.handleIndex))
	mux.HandleFunc("/clients", auth.requireAdmin(d.handleClients))
	mux.HandleFunc("/connectors", auth.requireAdmin(d.handleConnectors))
	mux.HandleFunc("/users", auth.requireAdmin(d.handleUsers))
	mux.HandleFunc("/sessions", auth.requireAdmin(d.handleSessions))

	// Writes. Every one of them sits behind a session, the CSRF token and write
	// permission; the GET forms behind a session and write permission, so a
	// read-only account is never shown a button it cannot use.
	write := func(h http.HandlerFunc) http.HandlerFunc {
		return auth.requireAdmin(auth.requireWrite(auth.requireCSRF(h)))
	}
	form := func(h http.HandlerFunc) http.HandlerFunc {
		return auth.requireAdmin(auth.requireWrite(h))
	}
	mux.HandleFunc("/sessions/revoke", write(d.handleRevokeRefresh))
	mux.HandleFunc("/clients/new", form(d.handleClientForm))
	mux.HandleFunc("/clients/edit", form(d.handleClientForm))
	mux.HandleFunc("/clients/save", write(d.handleClientSave))
	// Delete answers GET with a confirmation page and POST with the deletion, so
	// only the POST carries the CSRF check.
	mux.HandleFunc("/clients/delete", form(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			auth.requireCSRF(d.handleClientDelete)(w, r)
			return
		}
		d.handleClientDelete(w, r)
	}))
	mux.HandleFunc("/users/new", form(d.handleUserForm))
	mux.HandleFunc("/users/edit", form(d.handleUserForm))
	mux.HandleFunc("/users/save", write(d.handleUserSave))
	mux.HandleFunc("/users/delete", form(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			auth.requireCSRF(d.handleUserDelete)(w, r)
			return
		}
		d.handleUserDelete(w, r)
	}))

	srv := &http.Server{
		Addr:              c.Listen,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("dex-dashboard listening", "addr", c.Listen, "dex", c.Dex.GRPCAddress, "issuer", c.OIDC.Issuer)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// newAuthenticatorWithRetry gives dex a minute to come up before giving up on
// its discovery document.
func newAuthenticatorWithRetry(ctx context.Context, c *Config, logger *slog.Logger) (*authenticator, error) {
	var lastErr error
	for attempt := 0; attempt < 12; attempt++ {
		auth, err := newAuthenticator(ctx, c, logger)
		if err == nil {
			return auth, nil
		}
		lastErr = err
		logger.Warn("waiting for dex's discovery endpoint", "issuer", c.OIDC.Issuer, "err", err)
		time.Sleep(5 * time.Second)
	}
	return nil, lastErr
}

// securityHeaders locks the panel down: it renders its own HTML and nothing
// else, so everything external is denied.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}
