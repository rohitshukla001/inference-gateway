// Package metrics defines the services' Prometheus collectors. They live on a
// private Registry, not the global one, so test instances can coexist.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics groups every collector the services emit.
type Metrics struct {
	registry *prometheus.Registry

	// HTTPRequestsTotal counts gateway HTTP requests by method, route and
	// status code. Route is templated to keep cardinality bounded (no raw
	// job ids).
	HTTPRequestsTotal *prometheus.CounterVec

	// HTTPRequestDuration is gateway HTTP latency, labelled like the counter.
	HTTPRequestDuration *prometheus.HistogramVec

	// InferenceLatency is orchestrator end-to-end latency by tenant, model,
	// provider and status.
	InferenceLatency *prometheus.HistogramVec

	// CacheHits and CacheMisses count exact-cache results per tenant+model.
	CacheHits   *prometheus.CounterVec
	CacheMisses *prometheus.CounterVec

	// ProviderFailures counts upstream provider errors per provider and model.
	ProviderFailures *prometheus.CounterVec

	// InferenceCost accumulates USD spend for dashboards; the budget guard
	// reads Postgres, not this.
	InferenceCost *prometheus.CounterVec
}

// New returns a Metrics with all collectors, plus the Go and process
// collectors, registered on a fresh Registry.
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

// Registry returns the underlying Prometheus registry.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// Handler serves this registry in the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
