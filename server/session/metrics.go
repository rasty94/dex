package session

import "github.com/prometheus/client_golang/prometheus"

// Metrics counts browser sessions. It is the fork's: upstream exports no session
// metrics. A nil *Metrics counts nothing, so the manager behaves the same
// without a Prometheus registry.
type Metrics struct {
	created prometheus.Counter
	expired *prometheus.CounterVec
}

// NewMetrics registers dex_auth_sessions_created_total and
// dex_auth_sessions_expired_total on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	created := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dex_auth_sessions_created_total",
		Help: "Browser sessions created.",
	})
	expired := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dex_auth_sessions_expired_total",
		Help: "Browser sessions that ran out, by how they were found: idle or absolute when the browser " +
			"presents an expired one, collected when garbage collection removes one nobody came back for. " +
			"The three add up to every expiry; sessions ended by an administrator are not counted here.",
	}, []string{"reason"})
	for _, c := range []prometheus.Collector{created, expired} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	for _, reason := range []string{"idle", "absolute", "collected"} {
		expired.WithLabelValues(reason)
	}
	return &Metrics{created: created, expired: expired}, nil
}

func (m *Metrics) sessionCreated() {
	if m == nil {
		return
	}
	m.created.Inc()
}

// sessionExpired takes the reason expiryReason gives.
func (m *Metrics) sessionExpired(reason string) {
	if m == nil {
		return
	}
	// expiryReason's wording; TestExpiryReasonsMapToLabels catches a drift.
	label := "idle"
	if reason == "absolute lifetime" {
		label = "absolute"
	}
	m.expired.WithLabelValues(label).Inc()
}

// Collected counts the sessions a garbage collection run removed.
func (m *Metrics) Collected(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.expired.WithLabelValues("collected").Add(float64(n))
}
