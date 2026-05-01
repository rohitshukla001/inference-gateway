// Package gateway implements the HTTP API layer that translates REST requests
// into gRPC calls to the orchestrator service.
package gateway

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Handler holds the HTTP handlers for the gateway API.
type Handler struct {
	orchestratorClient pb.InferenceServiceClient
}

// NewHandler creates a new gateway handler with the given orchestrator gRPC client.
func NewHandler(client pb.InferenceServiceClient) *Handler {
	return &Handler{
		orchestratorClient: client,
	}
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

// --- HTTP Handlers ---

// HandleExecute handles POST /v1/inference/execute.
func (h *Handler) HandleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	log.Printf("[gateway] received inference request | prompt_len=%d model=%s", len(req.Prompt), req.Model)

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
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
		code, msg := mapGRPCError(err)
		writeError(w, code, msg)
		return
	}

	resp := ExecuteResponse{
		Output:   grpcResp.GetOutput(),
		Model:    grpcResp.GetModel(),
		Provider: grpcResp.GetProvider(),
		Cached:   grpcResp.GetCached(),
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

	writeJSON(w, http.StatusOK, resp)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, ErrorResponse{Error: message, Code: code})
}

// mapGRPCError translates an upstream gRPC error into an HTTP status + a
// safe message. The orchestrator's status.Error contracts define the
// mapping; unknown codes default to 502 Bad Gateway.
func mapGRPCError(err error) (int, string) {
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusBadGateway, "inference execution failed"
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return http.StatusBadRequest, st.Message()
	case codes.Unauthenticated:
		return http.StatusUnauthorized, st.Message()
	case codes.PermissionDenied:
		return http.StatusForbidden, st.Message()
	case codes.ResourceExhausted:
		// Reserved for the Phase 3 budget guard.
		return http.StatusPaymentRequired, st.Message()
	default:
		return http.StatusBadGateway, "inference execution failed"
	}
}
