package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/gateway/middleware"
	"github.com/rshuklas/inference-gateway/internal/tenant"
)

func TestOversizedBodyIs413(t *testing.T) {
	h := NewHandler(nil, &stubJobsService{})
	body := `{"prompt":"` + strings.Repeat("a", 2048) + `"}`

	for name, hf := range map[string]http.HandlerFunc{
		"execute":       h.HandleExecute,
		"execute_async": h.HandleSubmitAsync,
	} {
		t.Run(name, func(t *testing.T) {
			r := newAuthedRequest(http.MethodPost, "/", []byte(body), &tenant.Tenant{ID: "t1"})
			rr := httptest.NewRecorder()
			middleware.MaxBody(1024)(hf).ServeHTTP(rr, r)
			if rr.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("got %d, want 413", rr.Code)
			}
		})
	}
}
