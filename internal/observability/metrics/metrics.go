// Package metrics defines the Prometheus collectors the inference gateway
// exposes. All collectors live on a private Registry rather than the global
// default so multiple test instances can coexist and so the binary controls
// exactly which metrics ship.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics groups every collector the services emit. Pass it down to the
// instrumentation seams; tests construct their own.
type Metrics struct {
	registry *prometheus.Registry

	// HTTPRequestsTotal counts gateway HTTP requests by method, route
	// template, and response status class. Route is templated to keep
	// cardinality bounded (no raw job ids in label values).
	HTTPRequestsTotal *prometheus.CounterVec

	// HTTPRequestDuration is the histogram of gateway HTTP request
	// durations in seconds, labelled the same way as the counter.
	HTTPRequestDuration *prometheus.HistogramVec

	// InferenceLatency is the orchestrator-side end-to-end latency in
	// seconds, broken down by tenant, model, provider, and outcome.
	InferenceLatency *prometheus.HistogramVec

	// CacheHits and CacheMisses count exact-cache results per tenant+model.
	CacheHits   *prometheus.CounterVec
	CacheMisses *prometheus.CounterVec

	// ProviderFailures counts upstream provider errors. Useful for
	// alerting on a single provider going degraded.
	ProviderFailures *prometheus.CounterVec

	// InferenceCost accumulates USD spend per tenant+model+provider for
	// dashboards; the budget guard reads from Postgres, not from this.
	InferenceCost *prometheus.CounterVec
}

// New constructs a Metrics with all collectors registered on a fresh
// Registry. The Go and process collectors come along for free so we get
// goroutine count, GC pauses, etc., without extra wiring.
func New() *Metrics {
	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: r,

		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_http_requests_total",
				Help: "Total HTTP requests handled by the gateway.",
			},
			[]string{"method", "route", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_http_request_duration_seconds",
				Help:    "Gateway HTTP request duration in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "route", "status"},
		),

		InferenceLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "inference_latency_seconds",
				Help:    "Orchestrator end-to-end inference latency in seconds.",
				Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"tenant", "model", "provider", "status"},
		),

		CacheHits: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cache_hits_total",
				Help: "Exact-cache hits.",
			},
			[]string{"tenant", "model"},
		),
		CacheMisses: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cache_misses_total",
				Help: "Exact-cache misses.",
			},
			[]string{"tenant", "model"},
		),

		ProviderFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "provider_failures_total",
				Help: "Upstream provider errors.",
			},
			[]string{"provider", "model"},
		),

		InferenceCost: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "inference_cost_usd_total",
				Help: "Cumulative inference cost in USD.",
			},
			[]string{"tenant", "model", "provider"},
		),
	}

	r.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.InferenceLatency,
		m.CacheHits,
		m.CacheMisses,
		m.ProviderFailures,
		m.InferenceCost,
	)

	return m
}

// Registry returns the underlying Prometheus registry. Used by the
// /metrics handler and by tests that gather the snapshot.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// Handler returns an http.Handler that serves the Prometheus exposition
// format from this Metrics' registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
