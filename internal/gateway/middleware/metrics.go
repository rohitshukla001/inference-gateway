package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rshuklas/inference-gateway/internal/observability/metrics"
)

// RouteTemplater turns a request URL path into a low-cardinality label
// value. Unknown paths return "other" so a hostile client can't blow up
// our cardinality by hitting random URLs.
type RouteTemplater func(path string) string

// DefaultRouteTemplater is the gateway's known path set. Anything outside
// the registered handlers is collapsed.
func DefaultRouteTemplater(path string) string {
	switch path {
	case "/health", "/metrics", "/v1/inference/execute", "/v1/inference/execute_async":
		return path
	}
	if len(path) >= len("/v1/jobs/") && path[:len("/v1/jobs/")] == "/v1/jobs/" {
		return "/v1/jobs/{id}"
	}
	return "other"
}

// statusCapturingWriter snoops on WriteHeader so we can label by response
// status. It defaults to 200 because handlers are allowed to skip
// WriteHeader before writing a body.
type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Metrics returns a middleware that records gateway HTTP request count and
// duration to m. Pass templater=nil to use the default.
func Metrics(m *metrics.Metrics, templater RouteTemplater) func(http.Handler) http.Handler {
	if templater == nil {
		templater = DefaultRouteTemplater
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			route := templater(r.URL.Path)

			scw := &statusCapturingWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(scw, r)

			duration := time.Since(start).Seconds()
			statusLabel := strconv.Itoa(scw.status)

			m.HTTPRequestsTotal.WithLabelValues(r.Method, route, statusLabel).Inc()
			m.HTTPRequestDuration.WithLabelValues(r.Method, route, statusLabel).Observe(duration)
		})
	}
}
