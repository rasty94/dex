package mfa

import "github.com/prometheus/client_golang/prometheus"

// Metrics counts second-factor checks. It is the fork's: upstream exports no MFA
// metrics. A nil *Metrics counts nothing, so the handler behaves the same
// without a Prometheus registry.
type Metrics struct {
	verifications *prometheus.CounterVec
}

// NewMetrics registers dex_mfa_verifications_total on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dex_mfa_verifications_total",
		Help: "Second-factor checks by method (totp, webauthn) and result (success, failure). " +
			"Every TOTP code is one, the first one at enrollment included; enrolling a WebAuthn key is not.",
	}, []string{"method", "result"})
	if err := reg.Register(v); err != nil {
		return nil, err
	}
	// Every series exists from the start, so a rate over failures reads 0 and
	// not "no data" until the first one happens.
	for _, method := range []string{"totp", "webauthn"} {
		for _, result := range []string{"success", "failure"} {
			v.WithLabelValues(method, result)
		}
	}
	return &Metrics{verifications: v}, nil
}

func (m *Metrics) verified(method string, ok bool) {
	if m == nil {
		return
	}
	result := "failure"
	if ok {
		result = "success"
	}
	m.verifications.WithLabelValues(method, result).Inc()
}
