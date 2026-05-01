package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/jobs"
	"github.com/rshuklas/inference-gateway/internal/tenant"
)

type stubJobsService struct {
	submitFn func(ctx context.Context, tenantID string, payload []byte) (string, error)
	getFn    func(ctx context.Context, tenantID, jobID string) (*jobs.Job, error)

	lastTenant  string
	lastPayload []byte
	lastJobID   string
}

func (s *stubJobsService) Submit(ctx context.Context, tenantID string, payload []byte) (string, error) {
	s.lastTenant = tenantID
	s.lastPayload = payload
	if s.submitFn != nil {
		return s.submitFn(ctx, tenantID, payload)
	}
	return "job-1", nil
}

func (s *stubJobsService) Get(ctx context.Context, tenantID, jobID string) (*jobs.Job, error) {
	s.lastTenant = tenantID
	s.lastJobID = jobID
	if s.getFn != nil {
		return s.getFn(ctx, tenantID, jobID)
	}
	return nil, jobs.ErrNotFound
}

func newAuthedRequest(method, path string, body []byte, t *tenant.Tenant) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if t != nil {
		r = r.WithContext(tenant.WithTenant(r.Context(), t))
	}
	return r
}

func TestHandleSubmitAsync_Success(t *testing.T) {
	svc := &stubJobsService{}
	h := NewHandler(nil, svc)

	body, _ := json.Marshal(ExecuteRequest{Prompt: "hi"})
	rr := httptest.NewRecorder()
	h.HandleSubmitAsync(rr, newAuthedRequest(http.MethodPost,
		"/v1/inference/execute_async", body,
		&tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var resp SubmitAsyncResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID != "job-1" {
		t.Errorf("expected job-1, got %q", resp.JobID)
	}
	if resp.Status != "pending" {
		t.Errorf("expected status pending, got %q", resp.Status)
	}
	if svc.lastTenant != "tenant-1" {
		t.Errorf("expected tenant-1 forwarded, got %q", svc.lastTenant)
	}
}

func TestHandleSubmitAsync_RejectsEmptyPrompt(t *testing.T) {
	svc := &stubJobsService{}
	h := NewHandler(nil, svc)

	body, _ := json.Marshal(ExecuteRequest{Prompt: ""})
	rr := httptest.NewRecorder()
	h.HandleSubmitAsync(rr, newAuthedRequest(http.MethodPost,
		"/v1/inference/execute_async", body,
		&tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleSubmitAsync_RejectsWrongMethod(t *testing.T) {
	h := NewHandler(nil, &stubJobsService{})
	rr := httptest.NewRecorder()
	h.HandleSubmitAsync(rr, newAuthedRequest(http.MethodGet,
		"/v1/inference/execute_async", nil,
		&tenant.Tenant{ID: "t"}))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleSubmitAsync_NoTenantContext_Rejects(t *testing.T) {
	h := NewHandler(nil, &stubJobsService{})
	body, _ := json.Marshal(ExecuteRequest{Prompt: "hi"})
	rr := httptest.NewRecorder()
	h.HandleSubmitAsync(rr,
		httptest.NewRequest(http.MethodPost, "/v1/inference/execute_async", bytes.NewReader(body)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without tenant, got %d", rr.Code)
	}
}

func TestHandleSubmitAsync_NoJobsService_Returns503(t *testing.T) {
	h := NewHandler(nil, nil)
	body, _ := json.Marshal(ExecuteRequest{Prompt: "hi"})
	rr := httptest.NewRecorder()
	h.HandleSubmitAsync(rr, newAuthedRequest(http.MethodPost,
		"/v1/inference/execute_async", body,
		&tenant.Tenant{ID: "t"}))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when async disabled, got %d", rr.Code)
	}
}

func TestHandleSubmitAsync_PropagatesPayload(t *testing.T) {
	svc := &stubJobsService{}
	h := NewHandler(nil, svc)

	body, _ := json.Marshal(ExecuteRequest{
		Prompt:       "hi",
		Model:        "gpt-4o-mini",
		MaxCost:      0.01,
		LatencySLAMs: 500,
	})
	rr := httptest.NewRecorder()
	h.HandleSubmitAsync(rr, newAuthedRequest(http.MethodPost,
		"/v1/inference/execute_async", body,
		&tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	var got ExecuteRequest
	if err := json.Unmarshal(svc.lastPayload, &got); err != nil {
		t.Fatalf("decode forwarded payload: %v", err)
	}
	if got.Prompt != "hi" || got.Model != "gpt-4o-mini" || got.MaxCost != 0.01 || got.LatencySLAMs != 500 {
		t.Errorf("payload not preserved: %+v", got)
	}
}

func TestHandleJobStatus_PendingResponse(t *testing.T) {
	created := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc := &stubJobsService{
		getFn: func(_ context.Context, _, _ string) (*jobs.Job, error) {
			return &jobs.Job{
				JobID:     "job-1",
				TenantID:  "tenant-1",
				Status:    jobs.StatusPending,
				CreatedAt: created,
			}, nil
		},
	}
	h := NewHandler(nil, svc)

	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/job-1", nil, &tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp JobStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "pending" {
		t.Errorf("expected pending, got %q", resp.Status)
	}
	if resp.Result != nil {
		t.Error("expected no result for pending job")
	}
	if svc.lastJobID != "job-1" || svc.lastTenant != "tenant-1" {
		t.Errorf("forwarded wrong key: tenant=%q job=%q", svc.lastTenant, svc.lastJobID)
	}
}

func TestHandleJobStatus_CompletedDecodesResult(t *testing.T) {
	respPayload, _ := json.Marshal(ExecuteResponse{
		Output:   "hi back",
		Model:    "gpt-4o-mini",
		Provider: "mock",
		Cost:     0.0001,
	})
	completed := time.Date(2026, 5, 1, 12, 0, 5, 0, time.UTC)
	svc := &stubJobsService{
		getFn: func(_ context.Context, _, _ string) (*jobs.Job, error) {
			return &jobs.Job{
				JobID:           "job-1",
				TenantID:        "tenant-1",
				Status:          jobs.StatusCompleted,
				ResponsePayload: respPayload,
				CompletedAt:     &completed,
			}, nil
		},
	}
	h := NewHandler(nil, svc)

	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/job-1", nil, &tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp JobStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("expected completed, got %q", resp.Status)
	}
	if resp.Result == nil {
		t.Fatal("expected result populated for completed job")
	}
	if resp.Result.Output != "hi back" || resp.Result.Model != "gpt-4o-mini" {
		t.Errorf("result mismatch: %+v", resp.Result)
	}
}

func TestHandleJobStatus_FailedSurfacesError(t *testing.T) {
	svc := &stubJobsService{
		getFn: func(_ context.Context, _, _ string) (*jobs.Job, error) {
			return &jobs.Job{
				JobID:        "job-1",
				TenantID:     "tenant-1",
				Status:       jobs.StatusFailed,
				ErrorMessage: "provider timeout",
			}, nil
		},
	}
	h := NewHandler(nil, svc)

	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/job-1", nil, &tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp JobStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "failed" || resp.Error != "provider timeout" {
		t.Errorf("expected failed with error, got status=%q error=%q", resp.Status, resp.Error)
	}
}

func TestHandleJobStatus_NotFoundReturns404(t *testing.T) {
	svc := &stubJobsService{
		getFn: func(_ context.Context, _, _ string) (*jobs.Job, error) {
			return nil, jobs.ErrNotFound
		},
	}
	h := NewHandler(nil, svc)

	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/missing", nil, &tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleJobStatus_BackendErrorReturns500(t *testing.T) {
	svc := &stubJobsService{
		getFn: func(_ context.Context, _, _ string) (*jobs.Job, error) {
			return nil, errors.New("db unreachable")
		},
	}
	h := NewHandler(nil, svc)

	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/job-1", nil, &tenant.Tenant{ID: "tenant-1"}))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

func TestHandleJobStatus_RejectsEmptyJobID(t *testing.T) {
	h := NewHandler(nil, &stubJobsService{})
	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/", nil, &tenant.Tenant{ID: "t"}))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleJobStatus_RejectsNestedPath(t *testing.T) {
	h := NewHandler(nil, &stubJobsService{})
	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/job-1/extra", nil, &tenant.Tenant{ID: "t"}))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// confirms the response body includes the documented top-level keys
// (caught a bug in development where omitempty hid status).
func TestHandleJobStatus_ResponseShape(t *testing.T) {
	svc := &stubJobsService{
		getFn: func(_ context.Context, _, _ string) (*jobs.Job, error) {
			return &jobs.Job{
				JobID:    "j1",
				TenantID: "t",
				Status:   jobs.StatusRunning,
			}, nil
		},
	}
	h := NewHandler(nil, svc)
	rr := httptest.NewRecorder()
	h.HandleJobStatus(rr, newAuthedRequest(http.MethodGet,
		"/v1/jobs/j1", nil, &tenant.Tenant{ID: "t"}))

	body := rr.Body.String()
	for _, key := range []string{`"job_id"`, `"status"`, `"created_at"`} {
		if !strings.Contains(body, key) {
			t.Errorf("response missing %s: %s", key, body)
		}
	}
}
