// Package gateway implements the HTTP API, translating REST requests into
// orchestrator gRPC calls and async job submissions.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rshuklas/inference-gateway/internal/jobs"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// JobsService is the subset of jobs.Service the handler needs.
type JobsService interface {
	Submit(ctx context.Context, tenantID string, requestPayload []byte) (string, error)
	Get(ctx context.Context, tenantID, jobID string) (*jobs.Job, error)
}

// defaultRequestTimeout bounds a sync inference call when none is configured.
const defaultRequestTimeout = 120 * time.Second

// Handler holds the HTTP handlers for the gateway API.
type Handler struct {
	orchestratorClient pb.InferenceServiceClient
	jobs               JobsService
	requestTimeout     time.Duration
}

// NewHandler returns a Handler. jobsSvc may be nil, in which case the async
// endpoints return 503.
func NewHandler(client pb.InferenceServiceClient, jobsSvc JobsService) *Handler {
	return &Handler{
		orchestratorClient: client,
		jobs:               jobsSvc,
		requestTimeout:     defaultRequestTimeout,
	}
}

// WithRequestTimeout sets the deadline for sync inference calls.
func (h *Handler) WithRequestTimeout(d time.Duration) *Handler {
	if d > 0 {
		h.requestTimeout = d
	}
	return h
}

// --- Request/Response DTOs ---

// ExecuteRequest is the JSON body for POST /v1/inference/execute.
type ExecuteRequest struct {
	Prompt       string  `json:"prompt"`
	Model        string  `json:"model,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	MaxCost      float64 `json:"max_cost,omitempty"`
	LatencySLAMs int32   `json:"latency_sla_ms,omitempty"`
}

// ExecuteResponse is the JSON response for inference execution.
type ExecuteResponse struct {
	Output   string     `json:"output"`
	Model    string     `json:"model,omitempty"`
	Provider string     `json:"provider,omitempty"`
	Usage    *UsageInfo `json:"usage,omitempty"`
	Cached   bool       `json:"cached"`
	Cost     float64    `json:"cost"`
}

// UsageInfo maps token usage for the HTTP response.
type UsageInfo struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
}

// HealthResponse is the JSON response for GET /health.
type HealthResponse struct {
	Status       string              `json:"status"`
	Gateway      string              `json:"gateway"`
	Orchestrator *OrchestratorHealth `json:"orchestrator,omitempty"`
}

// OrchestratorHealth represents the orchestrator's health in the response.
type OrchestratorHealth struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ErrorResponse is the standard JSON error response.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

// SubmitAsyncResponse is returned from POST /v1/inference/execute_async.
type SubmitAsyncResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// JobStatusResponse is returned from GET /v1/jobs/{id}. Result and Error
// are present only when the job has completed or failed respectively.
type JobStatusResponse struct {
	JobID       string           `json:"job_id"`
	Status      string           `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
	Result      *ExecuteResponse `json:"result,omitempty"`
	Error       string           `json:"error,omitempty"`
}

// --- HTTP Handlers ---

// HandleExecute handles POST /v1/inference/execute.
func (h *Handler) HandleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ExecuteRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	log.Printf("[gateway] received inference request | prompt_len=%d model=%s", len(req.Prompt), req.Model)

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()

	grpcResp, err := h.orchestratorClient.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{
		Prompt:       req.Prompt,
		Model:        req.Model,
		Provider:     req.Provider,
		MaxCost:      req.MaxCost,
		LatencySlaMs: req.LatencySLAMs,
	})
	if err != nil {
		log.Printf("[gateway] orchestrator error: %v", err)
		code, msg := MapGRPCError(err)
		writeError(w, code, msg)
		return
	}

	resp := ExecuteResponse{
		Output:   grpcResp.GetOutput(),
		Model:    grpcResp.GetModel(),
		Provider: grpcResp.GetProvider(),
		Cached:   grpcResp.GetCached(),
		Cost:     grpcResp.GetCost(),
	}

	if usage := grpcResp.GetUsage(); usage != nil {
		resp.Usage = &UsageInfo{
			PromptTokens:     usage.GetPromptTokens(),
			CompletionTokens: usage.GetCompletionTokens(),
			TotalTokens:      usage.GetTotalTokens(),
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleHealth handles GET /health.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	resp := HealthResponse{
		Status:  "SERVING",
		Gateway: "healthy",
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	healthResp, err := h.orchestratorClient.HealthCheck(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		log.Printf("[gateway] orchestrator health check failed: %v", err)
		resp.Orchestrator = &OrchestratorHealth{Status: "NOT_SERVING"}
	} else {
		resp.Orchestrator = &OrchestratorHealth{
			Status:  healthResp.GetStatus(),
			Version: healthResp.GetVersion(),
		}
	}

	// The gateway cannot serve inference without a serving orchestrator, so
	// report that to load balancers instead of a blanket 200.
	code := http.StatusOK
	if resp.Orchestrator.Status != "SERVING" {
		resp.Status = "NOT_SERVING"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, resp)
}

// HandleSubmitAsync handles POST /v1/inference/execute_async: it persists and
// enqueues a pending job and returns its job_id with 202.
func (h *Handler) HandleSubmitAsync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "async endpoint not enabled")
		return
	}

	t := tenant.FromContext(r.Context())
	if t == nil {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	var req ExecuteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		log.Printf("[gateway] async marshal failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to encode request")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	jobID, err := h.jobs.Submit(ctx, t.ID, payload)
	if err != nil {
		log.Printf("[gateway] async submit failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to submit job")
		return
	}

	log.Printf("[gateway] async job submitted | tenant=%s job=%s", t.ID, jobID)

	writeJSON(w, http.StatusAccepted, SubmitAsyncResponse{
		JobID:  jobID,
		Status: string(jobs.StatusPending),
	})
}

// HandleJobStatus handles GET /v1/jobs/{job_id}.
func (h *Handler) HandleJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "async endpoint not enabled")
		return
	}

	t := tenant.FromContext(r.Context())
	if t == nil {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	jobID := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	jobID = strings.TrimSuffix(jobID, "/")
	if jobID == "" || strings.Contains(jobID, "/") {
		writeError(w, http.StatusBadRequest, "job_id is required")
		return
	}
	// Job IDs are UUIDs; anything else cannot exist, and passing it to
	// Postgres would fail the uuid cast and surface as a 500.
	if _, err := uuid.Parse(jobID); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	job, err := h.jobs.Get(ctx, t.ID, jobID)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		log.Printf("[gateway] job get failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}

	resp := JobStatusResponse{
		JobID:       job.JobID,
		Status:      string(job.Status),
		CreatedAt:   job.CreatedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
		Error:       job.ErrorMessage,
	}

	if job.Status == jobs.StatusCompleted && len(job.ResponsePayload) > 0 {
		var result ExecuteResponse
		if err := json.Unmarshal(job.ResponsePayload, &result); err != nil {
			log.Printf("[gateway] response payload decode failed for job=%s: %v", job.JobID, err)
		} else {
			resp.Result = &result
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Helpers ---

// decodeBody decodes the JSON body into dst, writing 413 for an oversized
// body and 400 for anything else that fails. It reports whether to continue.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[gateway] write response failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, ErrorResponse{Error: message, Code: code})
}

// MapGRPCError maps an orchestrator gRPC error to an HTTP status and a
// client-safe message. Unmapped codes become 502 with a generic message so
// internal details never leak.
func MapGRPCError(err error) (int, string) {
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusBadGateway, "inference execution failed"
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return http.StatusBadRequest, st.Message()
	case codes.PermissionDenied:
		return http.StatusForbidden, st.Message()
	case codes.ResourceExhausted:
		// Monthly budget exceeded.
		return http.StatusPaymentRequired, st.Message()
	case codes.Unavailable:
		return http.StatusServiceUnavailable, "service temporarily unavailable"
	default:
		return http.StatusBadGateway, "inference execution failed"
	}
}
