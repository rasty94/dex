package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleMetrics = `# HELP http_requests_total Count of all HTTP requests.
# TYPE http_requests_total counter
http_requests_total{code="200",handler="/token",method="post"} 40
http_requests_total{code="401",handler="/token",method="post"} 5
http_requests_total{code="500",handler="/token",method="post"} 2
http_requests_total{code="200",handler="/keys",method="get"} 7
# HELP login_rate_limited_total Count of login attempts refused by the login rate limiter.
# TYPE login_rate_limited_total counter
login_rate_limited_total 3
# HELP keystone_login_attempts_total Count of Keystone login attempts by step and outcome.
# TYPE keystone_login_attempts_total counter
keystone_login_attempts_total{outcome="success",step="password"} 11
keystone_login_attempts_total{outcome="failure",step="totp"} 1
# HELP request_duration_seconds A histogram of latencies for requests.
# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{le="0.25"} 30
request_duration_seconds_sum 12.5
request_duration_seconds_count 47
`

func TestTelemetryMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			w.Write([]byte(sampleMetrics))
		case "/healthz":
			w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tel := newTelemetry(srv.URL)
	var data StatusData
	tel.metrics(context.Background(), &data)

	if data.MetricsErr != "" {
		t.Fatalf("unexpected error: %s", data.MetricsErr)
	}
	if data.Requests != 54 {
		t.Errorf("total requests = %v, want 54", data.Requests)
	}
	// Both 4xx and 5xx count as errors: a flood of 401s on /token is as much a
	// signal as a 500, and often the more interesting one.
	if data.Errors != 7 {
		t.Errorf("errors = %v, want 7 (five 401s and two 500s)", data.Errors)
	}
	if data.RateLimited != 3 {
		t.Errorf("rate limited = %v, want 3", data.RateLimited)
	}

	// Endpoints are folded per handler and sorted by traffic.
	if len(data.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(data.Endpoints))
	}
	if data.Endpoints[0].Handler != "/token" || data.Endpoints[0].Total != 47 || data.Endpoints[0].Errors != 7 {
		t.Errorf("busiest endpoint = %+v, want /token with 47 requests and 7 errors", data.Endpoints[0])
	}

	// Keystone counters are summed across their labels; histograms are ignored.
	if len(data.Keystone) != 1 {
		t.Fatalf("keystone counters = %d, want 1", len(data.Keystone))
	}
	if data.Keystone[0].Value != 12 {
		t.Errorf("keystone total = %v, want 12", data.Keystone[0].Value)
	}
}

// Telemetry being unreachable must show as a message, not take the page down.
func TestTelemetryUnreachable(t *testing.T) {
	tel := newTelemetry("http://127.0.0.1:1")
	var data StatusData
	tel.metrics(context.Background(), &data)
	if data.MetricsErr == "" {
		t.Error("expected an error message when telemetry cannot be reached")
	}

	healthy, msg := tel.health(context.Background())
	if healthy || msg == "" {
		t.Errorf("health on an unreachable endpoint = %v, %q; want false with a reason", healthy, msg)
	}
}

func TestTelemetryDisabled(t *testing.T) {
	if newTelemetry("") != nil {
		t.Error("an empty telemetry URL should disable the feature, not build a client")
	}
}
