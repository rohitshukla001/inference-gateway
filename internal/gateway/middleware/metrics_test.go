package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rshuklas/inference-gateway/internal/observability/metrics"
)

func TestDefaultRouteTemplater(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/health", "/health"},
		{"/metrics", "/metrics"},
		{"/v1/inference/execute", "/v1/inference/execute"},
		{"/v1/inference/execute_async", "/v1/inference/execute_async"},
		{"/v1/jobs/9b2f3c50-1111-2222-3333-444444444444", "/v1/jobs/{id}"},
		{"/v1/jobs/", "/v1/jobs/{id}"},
		{"/totally/random", "other"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := DefaultRouteTemplater(tc.path)
			if got != tc.want {
				t.Errorf("templater(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestMetricsMiddleware_RecordsRequestAndDuration(t *testing.T) {
	m := metrics.New()

	handler := Metrics(m, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/inference/execute", nil))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 propagated, got %d", rr.Code)
	}

	got := gatherMetric(t, m.Registry(), "gateway_http_requests_total")
	if !strings.Contains(got, `route="/v1/inference/execute"`) ||
		!strings.Contains(got, `status="201"`) ||
		!strings.Contains(got, `method="POST"`) {
		t.Errorf("expected counter labels for POST /v1/inference/execute 201, got:\n%s", got)
	}

	histo := gatherMetric(t, m.Registry(), "gateway_http_request_duration_seconds_count")
	if !strings.Contains(histo, "1") {
		t.Errorf("expected duration count 1, got:\n%s", histo)
	}
}

func TestMetricsMiddleware_DefaultStatusIs200(t *testing.T) {
	m := metrics.New()
	handler := Metrics(m, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// no WriteHeader call: net/http defaults to 200
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	got := gatherMetric(t, m.Registry(), "gateway_http_requests_total")
	if !strings.Contains(got, `status="200"`) {
		t.Errorf("expected default status 200, got:\n%s", got)
	}
}

// gatherMetric returns the text exposition format for the named metric so
// assertions can substring-match. Avoids pulling in the full prometheus
// testutil dependency for one test file.
func gatherMetric(t *testing.T, reg *prometheus.Registry, name string) string {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var b strings.Builder
	for _, mf := range mfs {
		if !strings.HasPrefix(mf.GetName(), strings.TrimSuffix(name, "_count")) {
			continue
		}
		for _, m := range mf.GetMetric() {
			b.WriteString(mf.GetName())
			b.WriteString("{")
			for _, l := range m.GetLabel() {
				b.WriteString(l.GetName())
				b.WriteString(`="`)
				b.WriteString(l.GetValue())
				b.WriteString(`",`)
			}
			b.WriteString("}\n")
		}
	}
	return b.String()
}
