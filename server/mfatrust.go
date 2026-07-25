package server

import (
	"net/http"
	"strings"
)

const mfaTrustCookiePrefix = "dex_mfa_trust_"

// The upstream provider (Keystone) enforces the second factor itself, so dex
// cannot skip it on its own. Instead, a trusted device keeps the token issued
// after a successful second factor login in a cookie, and the next login
// revalidates that token against the provider rather than asking for the
// password and passcode again. The device therefore stays trusted only while
// the provider keeps the token valid, and revoking it upstream ends the trust
// immediately.
func mfaTrustCookieName(connID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '_'
		}
	}, connID)
	return mfaTrustCookiePrefix + safe
}

func (s *Server) mfaTrustCookie(connID, token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     mfaTrustCookieName(connID),
		Value:    token,
		Path:     s.absPath("/auth"),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.issuerURL.Scheme == "https",
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) setMFATrustCookie(w http.ResponseWriter, connID, token string) {
	http.SetCookie(w, s.mfaTrustCookie(connID, token, int(s.mfaTrust.Duration.Seconds())))
}

func (s *Server) clearMFATrustCookie(w http.ResponseWriter, connID string) {
	http.SetCookie(w, s.mfaTrustCookie(connID, "", -1))
}

// mfaTrustToken returns the upstream token of a trusted device, or "" if this
// device is not trusted for connID.
func (s *Server) mfaTrustToken(r *http.Request, connID string) string {
	c, err := r.Cookie(mfaTrustCookieName(connID))
	if err != nil {
		return ""
	}
	return c.Value
}
