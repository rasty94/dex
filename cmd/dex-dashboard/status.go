package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// telemetry reads dex's telemetry endpoint: the health check and the Prometheus
// metrics. It is a plain HTTP client because that endpoint has no auth of its
// own — which is also why the dashboard must not proxy it verbatim to a browser.
type telemetry struct {
	baseURL string
	client  *http.Client
}

func newTelemetry(baseURL string) *telemetry {
	if baseURL == "" {
		return nil
	}
	return &telemetry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (t *telemetry) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("telemetry returned %s", resp.Status)
	}
	return body, nil
}

// health reports whether dex's own health check passes.
func (t *telemetry) health(ctx context.Context) (bool, string) {
	body, err := t.get(ctx, "/healthz")
	if err != nil {
		return false, err.Error()
	}
	return true, strings.TrimSpace(string(body))
}

// endpointStat is one dex HTTP handler's traffic.
type endpointStat struct {
	Handler string
	Total   float64
	Errors  float64
}

// StatusData is the operations view.
type StatusData struct {
	Healthy   bool
	HealthMsg string

	Requests  float64
	Errors    float64
	Endpoints []endpointStat

	RateLimited float64
	Keystone    []counter

	MetricsErr string
}

type counter struct {
	Name  string
	Help  string
	Value float64
}

// metrics scrapes and folds dex's counters into what an operator would actually
// look at: traffic, errors per endpoint, refused logins, and whatever the
// keystone connector reports.
func (t *telemetry) metrics(ctx context.Context, data *StatusData) {
	body, err := t.get(ctx, "/metrics")
	if err != nil {
		data.MetricsErr = err.Error()
		return
	}

	// The zero TextParser carries no name validation scheme and panics on the
	// first metric, so it has to be built with one. UTF-8 is what Prometheus
	// itself defaults to.
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	if err != nil {
		data.MetricsErr = "could not parse dex's metrics: " + err.Error()
		return
	}

	byHandler := map[string]*endpointStat{}
	if family := families["http_requests_total"]; family != nil {
		for _, m := range family.Metric {
			handler, code := "", ""
			for _, l := range m.Label {
				switch l.GetName() {
				case "handler":
					handler = l.GetValue()
				case "code":
					code = l.GetValue()
				}
			}
			v := counterValue(m)
			data.Requests += v

			stat := byHandler[handler]
			if stat == nil {
				stat = &endpointStat{Handler: handler}
				byHandler[handler] = stat
			}
			stat.Total += v
			// 4xx and 5xx both count: a flood of 400s on /token is as much a
			// signal as a 500, and often the more interesting one.
			if strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5") {
				stat.Errors += v
				data.Errors += v
			}
		}
	}
	for _, stat := range byHandler {
		data.Endpoints = append(data.Endpoints, *stat)
	}
	sort.Slice(data.Endpoints, func(i, j int) bool {
		return data.Endpoints[i].Total > data.Endpoints[j].Total
	})

	if family := families["login_rate_limited_total"]; family != nil {
		for _, m := range family.Metric {
			data.RateLimited += counterValue(m)
		}
	}

	for name, family := range families {
		if !strings.HasPrefix(name, "keystone_") || family.GetType() != dto.MetricType_COUNTER {
			continue
		}
		var total float64
		for _, m := range family.Metric {
			total += counterValue(m)
		}
		data.Keystone = append(data.Keystone, counter{Name: name, Help: family.GetHelp(), Value: total})
	}
	sort.Slice(data.Keystone, func(i, j int) bool { return data.Keystone[i].Name < data.Keystone[j].Name })
}

func counterValue(m *dto.Metric) float64 {
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	return 0
}

// handleStatus renders the operations view. It degrades rather than failing:
// telemetry may be switched off, and that is worth saying plainly rather than
// returning a 500.
func (d *dashboard) handleStatus(w http.ResponseWriter, r *http.Request) {
	data := StatusData{}

	if d.telemetry == nil {
		data.MetricsErr = "dex's telemetry endpoint is not configured. Set dex.telemetryURL in the dashboard config, and telemetry.http in dex."
		d.render(w, r, "status.html", page{Title: "Status", Nav: "status", Data: data})
		return
	}

	data.Healthy, data.HealthMsg = d.telemetry.health(r.Context())
	d.telemetry.metrics(r.Context(), &data)
	d.render(w, r, "status.html", page{Title: "Status", Nav: "status", Data: data})
}
