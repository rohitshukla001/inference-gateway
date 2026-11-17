package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/tenant"
)

type stubResolver struct {
	wantKey string
	tenant  *tenant.Tenant
	err     error
}

func (s *stubResolver) GetByAPIKey(_ context.Context, key string) (*tenant.Tenant, error) {
	if s.err != nil {
		return nil, s.err
	}
	if key != s.wantKey {
		return nil, tenant.ErrNotFound
	}
	return s.tenant, nil
}

func newReq(authHeader string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/inference/execute", nil)
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	return r
}

func TestAuth_ValidKey_AttachesTenant(t *testing.T) {
	want := &tenant.Tenant{ID: "tid-1", Name: "ACME", Status: "active", Role: "user"}
	resolver := &stubResolver{wantKey: "good-key", tenant: want}

	var got *tenant.Tenant
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = tenant.FromContext(r.Context())
	})

	rr := httptest.NewRecorder()
	Auth(resolver)(next).ServeHTTP(rr, newReq("Bearer good-key"))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if got != want {
		t.Errorf("expected tenant attached, got %v", got)
	}
}

func TestAuth_MissingHeader_Rejects(t *testing.T) {
	resolver := &stubResolver{}
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	rr := httptest.NewRecorder()
	Auth(resolver)(next).ServeHTTP(rr, newReq(""))

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	if called {
		t.Error("next handler should not be called when auth fails")
	}
}

func TestAuth_MalformedHeader_Rejects(t *testing.T) {
	resolver := &stubResolver{}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	cases := []string{
		"not-bearer-scheme",
		"Basic dXNlcjpwYXNz",
		"Bearer",
		"Bearer  ", // empty token after scheme
	}

	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			rr := httptest.NewRecorder()
			Auth(resolver)(next).ServeHTTP(rr, newReq(h))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("header %q expected 401, got %d", h, rr.Code)
			}
		})
	}
}

func TestAuth_UnknownKey_Rejects(t *testing.T) {
	resolver := &stubResolver{wantKey: "good-key", tenant: &tenant.Tenant{ID: "tid-1"}}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	rr := httptest.NewRecorder()
	Auth(resolver)(next).ServeHTTP(rr, newReq("Bearer wrong-key"))

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuth_BackendError_Returns500(t *testing.T) {
	resolver := &stubResolver{err: errFakeBackend}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	rr := httptest.NewRecorder()
	Auth(resolver)(next).ServeHTTP(rr, newReq("Bearer any"))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

func TestAuth_LowercaseScheme_Accepted(t *testing.T) {
	want := &tenant.Tenant{ID: "tid-1"}
	resolver := &stubResolver{wantKey: "k", tenant: want}

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	rr := httptest.NewRecorder()
	Auth(resolver)(next).ServeHTTP(rr, newReq("bearer k"))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 with lowercase scheme, got %d", rr.Code)
	}
}

var errFakeBackend = stubError("backend exploded")

type stubError string

func (e stubError) Error() string { return string(e) }