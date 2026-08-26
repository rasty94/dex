package keystone

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics for the Keystone connector. They are plain package level collectors
// instead of per-connector fields because ConnectorConfig.Open does not receive
// the server's registry; cmd/dex registers Collectors() on it at startup.
//
// ponytail: labelled by result, not one counter per outcome. Adding an outcome
// is a new label value, not a new metric.
var (
	loginAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "keystone_login_attempts_total",
		Help: "Count of Keystone login attempts by step and outcome.",
	}, []string{"step", "result"})

	refreshAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "keystone_refresh_total",
		Help: "Count of Keystone refresh attempts by outcome.",
	}, []string{"result"})

	tokenValidations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "keystone_token_validations_total",
		Help: "Count of Keystone token validations by outcome.",
	}, []string{"result"})

	tokenValidationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "keystone_token_validation_duration_seconds",
		Help:    "Latency of token validation calls against the Keystone API.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"result"})

	tokenCacheLookups = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "keystone_token_cache_lookups_total",
		Help: "Count of token cache lookups by outcome. Absent when cacheTTL is unset.",
	}, []string{"result"})
)

// Collectors returns the connector metrics so the server can register them.
func Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		loginAttempts,
		refreshAttempts,
		tokenValidations,
		tokenValidationDuration,
		tokenCacheLookups,
	}
}

// loginResult maps the (validPassword, err) pair the connector returns onto a
// metric label. A missing second factor is not a failure: it is the first half
// of a two step login, and telling it apart from a wrong password is the whole
// point of measuring this.
func loginResult(validPassword bool, err error) string {
	switch {
	case err == nil && validPassword:
		return "success"
	case err == nil:
		return "invalid_credentials"
	default:
		if _, ok := err.(ErrTOTPRequired); ok {
			return "totp_required"
		}
		return "error"
	}
}

func result(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}
