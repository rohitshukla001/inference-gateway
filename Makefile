.PHONY: proto run-gateway run-orchestrator build up down clean test help

# ---- Proto Generation ----

proto:
	@echo "==> Generating protobuf Go code..."
	protoc \
		--go_out=proto --go_opt=paths=source_relative \
		--go-grpc_out=proto --go-grpc_opt=paths=source_relative \
		-I proto \
		proto/inference/v1/inference.proto
	@echo "==> Done."

proto-install:
	@echo "==> Installing protoc plugins..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "==> Done. Make sure $$GOPATH/bin is in your PATH."

# ---- Build ----

build:
	@echo "==> Building binaries..."
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/orchestrator ./cmd/orchestrator
	@echo "==> Done."

# ---- Run Services ----

run-gateway:
	@echo "==> Starting gateway on :8080..."
	go run ./cmd/gateway

run-orchestrator:
	@echo "==> Starting orchestrator on :9090..."
	go run ./cmd/orchestrator

# ---- Docker Compose (Infrastructure) ----

up:
	@echo "==> Starting infrastructure (Ollama, Postgres, Redis)..."
	docker-compose up -d
	@echo "==> Waiting for Ollama..."
	@sleep 3
	@echo "==> Infrastructure ready."

down:
	@echo "==> Stopping infrastructure..."
	docker-compose down

# ---- Testing ----

test:
	go test ./... -v -count=1

test-e2e:
	@echo "==> Running end-to-end test..."
	@echo "==> Checking gateway health..."
	curl -s http://localhost:8080/health | jq .
	@echo ""
	@echo "==> Executing inference..."
	curl -s -X POST http://localhost:8080/v1/inference/execute \
		-H "Content-Type: application/json" \
		-d '{"prompt": "Say hello in one sentence."}' | jq .

# ---- Utilities ----

clean:
	rm -rf bin/

pull-model:
	@echo "==> Pulling default model (llama3.2)..."
	docker exec ig-ollama ollama pull llama3.2
	@echo "==> Model ready."

deps:
	go mod tidy
	go mod download

# ---- Help ----

help:
	@echo "Inference Gateway - Makefile Commands"
	@echo ""
	@echo "  make proto              Generate protobuf Go code"
	@echo "  make proto-install      Install protoc Go plugins"
	@echo "  make build              Build gateway + orchestrator binaries"
	@echo "  make run-gateway        Run gateway service (HTTP :8080)"
	@echo "  make run-orchestrator   Run orchestrator service (gRPC :9090)"
	@echo "  make up                 Start infrastructure (Docker)"
	@echo "  make down               Stop infrastructure"
	@echo "  make test               Run unit tests"
	@echo "  make test-e2e           Run end-to-end smoke test"
	@echo "  make pull-model         Pull llama3.2 into Ollama"
	@echo "  make deps               Tidy and download Go modules"
	@echo "  make clean              Remove build artifacts"
