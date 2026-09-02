package authflow

// mfatrust.go implements the "remember this device" option on the second-factor
// step, for connectors whose provider enforces the factor itself.

import (
	"net/http"
	"strings"
	"time"

	"github.com/dexidp/dex/server/internal"
)

const mfaTrustCookiePrefix = "dex_mfa_trust_"

// MFATrustConfig configures the "remember this device" checkbox on the second
// factor step.
//
// The provider (Keystone) enforces the second factor itself, so dex cannot
// decide to skip it. Instead a trusted device keeps the token the provider
// issued after a successful second-factor login, and the next login revalidates
// that token with the provider rather than asking for the password and passcode
// again. The device stays trusted only while the provider keeps the token
// valid, so revoking it upstream ends the trust immediately.
//
// That token is a live upstream credential — it is meaningful to the whole
// OpenStack deployment, not just to dex — which is why EncryptionKey is
// required rather than optional.
type MFATrustConfig struct {
	Enabled bool `json:"enabled"`

	// Duration caps the trust cookie's lifetime. The effective lifetime is still
	// bounded by the provider's token expiry, which is usually much shorter.
	// Defaults to 720h (30 days).
	Duration time.Duration `json:"duration"`

	// EncryptionKey is the AES key (16, 24 or 32 bytes) the cookie is sealed
	// with. Required when Enabled.
	EncryptionKey []byte `json:"encryptionKey"`
}

// mfaTrustCookieName scopes the cookie to one connector, so trusting a device
// for one provider does not trust it for another. Connector IDs are
// user-supplied, so anything outside the cookie-name charset is folded to "_".
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

func (h *Handler) mfaTrustCookie(connID, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     mfaTrustCookieName(connID),
		Value:    value,
		Path:     h.IssuerURL.AbsPath("/auth"),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.IssuerURL.Scheme == "https",
		SameSite: http.SameSiteLaxMode,
	}
}

// setMFATrustCookie seals the provider's token into the trust cookie. A sealing
// failure drops the cookie rather than falling back to plaintext: losing the
// trust costs the user one extra second-factor prompt, while writing the token
// in the clear would hand a live upstream credential to anyone who reads it.
func (h *Handler) setMFATrustCookie(w http.ResponseWriter, connID, token string) {
	sealed, err := internal.EncryptCookieValue(token, h.MFATrust.EncryptionKey)
	if err != nil {
		h.Logger.Error("failed to encrypt the trusted device cookie, not trusting this device", "connector_id", connID, "err", err)
		return
	}
	http.SetCookie(w, h.mfaTrustCookie(connID, sealed, int(h.mfaTrust().Duration.Seconds())))
}

func (h *Handler) clearMFATrustCookie(w http.ResponseWriter, connID string) {
	http.SetCookie(w, h.mfaTrustCookie(connID, "", -1))
}

// mfaTrustToken returns the provider token of a trusted device, or "" when this
// device is not trusted for connID or the cookie does not open.
func (h *Handler) mfaTrustToken(r *http.Request, connID string) string {
	c, err := r.Cookie(mfaTrustCookieName(connID))
	if err != nil || c.Value == "" {
		return ""
	}
	token, err := internal.DecryptCookieValue(c.Value, h.MFATrust.EncryptionKey)
	if err != nil {
		// Tampered, or sealed with a key that has since been rotated. Either way
		// there is nothing to revalidate, so fall back to asking for credentials.
		h.Logger.DebugContext(r.Context(), "could not open the trusted device cookie", "connector_id", connID, "err", err)
		return ""
	}
	return token
}

// mfaTrust returns the config with defaults applied.
func (h *Handler) mfaTrust() MFATrustConfig {
	c := h.MFATrust
	if c.Duration <= 0 {
		c.Duration = 720 * time.Hour
	}
	return c
}
