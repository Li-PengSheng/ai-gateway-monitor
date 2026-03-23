# AI Microservice Demo

A production-style AI inference platform built as a portfolio project, demonstrating modern microservice patterns: REST-to-gRPC gateway, unary + streaming LLM inference, distributed tracing, Prometheus metrics, and Kubernetes deployment.

## Architecture

```
Client (HTTP)
     │
     ▼
┌─────────────────────────────────┐
│  go-gateway  (Gin · port 8080)  │  REST API gateway
│  • /predict/iris                │  • Prometheus metrics
│  • /predict/model               │  • OpenTelemetry tracing
│  • /predict/model/stream (SSE)  │  • pprof profiling (:6060)
│  • /health  · /metrics          │
└────────────┬────────────────────┘
             │ gRPC (port 50051)
             ▼
┌─────────────────────────────────┐
│  python-ai  (gRPC server)       │  ML inference backend
│  • IrisPredictor (scikit-learn) │  • OpenTelemetry tracing
│  • ModelPredictor (Ollama)      │  • Structured logging
└────────────┬────────────────────┘
             │ HTTP (port 11434)
             ▼
        Ollama (Qwen2.5:1.5b)      LLM running on host / GPU

Observability stack:
  Prometheus :9090  →  Grafana :3000
  Jaeger     :16686 (distributed traces)
Optional:
  NVIDIA GPU Exporter :9835
```

## Tech Stack

| Layer | Technology |
|---|---|
| API Gateway | Go 1.25 · Gin · gRPC client |
| ML Service | Python 3.12 · scikit-learn · Ollama |
| Communication | Protocol Buffers v3 · gRPC |
| Observability | Prometheus · Grafana · Jaeger (OpenTelemetry) |
| Container | Docker · Docker Compose |
| Orchestration | Kubernetes · HPA · ServiceMonitor |
| Load Testing | k6 |
| Code Gen | Buf |

## Quick Start

### Prerequisites

- Docker & Docker Compose
- [Ollama](https://ollama.com/) running on the host with `qwen2.5:1.5b` pulled:
  ```bash
  ollama pull qwen2.5:1.5b
  ```

### Run locally

```bash
# Clone and start all services
git clone https://github.com/Li-PengSheng/ai-microservice-demo.git
cd ai-microservice-demo
docker compose up --build
```

Services will be available at:

| Service | URL |
|---|---|
| API Gateway | http://localhost:8080 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 (admin/admin) |
| Jaeger UI | http://localhost:16686 |
| pprof | http://localhost:6060/debug/pprof |

### API Examples

```bash
# Iris flower classification
curl -X POST http://localhost:8080/predict/iris \
  -H 'Content-Type: application/json' \
  -d '{"sepal_length":6.0,"sepal_width":3.0,"petal_length":5.5,"petal_width":2.0}'
# → {"id":2,"result":"virginica","source":"Go Gateway -> Python AI (Iris)"}

# LLM inference (Qwen2.5)
curl -X POST http://localhost:8080/predict/model \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"Explain machine learning in one sentence."}'
# → {"metrics":{"duration_sec":...,"output_tokens":...,"prompt_tokens":...},"model":"qwen2.5:1.5b","reply":"...","source":"Go Gateway -> Ollama (Qwen)"}

# LLM streaming inference (Server-Sent Events)
curl -N -X POST http://localhost:8080/predict/model/stream \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"Explain machine learning in one sentence."}'
# → event: message / data: {...} chunks, then event: done

# Health check
curl http://localhost:8080/health
# → {"status":"ok"}
```

## Configuration

Environment variables for each service:

| Variable | Service | Default | Description |
|---|---|---|---|
| `HTTP_ADDR` | go-gateway | `:8080` | Go gateway HTTP listen address |
| `PPROF_ADDR` | go-gateway | `:6060` | Go pprof listen address |
| `AI_SERVICE_ADDR` | go-gateway | `localhost:50051` | Python gRPC backend address |
| `JAEGER_ENDPOINT` | go-gateway/python-ai | `localhost:4317` | OTLP endpoint for tracing export |
| `OLLAMA_HOST` | python-ai | `http://localhost:11434` | Ollama API base URL |
| `MODEL_NAME` | python-ai | `qwen2.5:1.5b` | Ollama model to serve |
| `IRIS_MODEL_PATH` | python-ai | _(unset)_ | Optional path to a pre-trained Iris model |

> In Docker Compose, these defaults are overridden where needed (for example `AI_SERVICE_ADDR=python-ai:50051`).

## Load Testing

Uses [k6](https://k6.io/) to simulate mixed traffic across both endpoints.

```bash
# Run load test (requires Docker)
docker run --rm -i --network host grafana/k6 run - < test/test.js
```

**Load profile:**

| Phase | Duration | VUs |
|---|---|---|
| Ramp-up | 15 s | 0 → 10 |
| Steady state | 30 s | 10 |
| Spike | 15 s | 10 → 30 |
| Hold spike | 30 s | 30 |
| Ramp-down | 10 s | 30 → 0 |

**Thresholds:**

- Iris p95 latency < 500 ms
- Model p95 latency < 30 s
- HTTP error rate < 1%

## Kubernetes Deployment

```bash
# Full local workflow (patch config, build images, apply manifests, start monitoring)
./deploy.sh

# Or run steps separately
./deploy.sh build
./deploy.sh apply
./deploy.sh monitor

# Tail logs
./deploy.sh logs

# Tear down
./deploy.sh reset
```

The K8s setup includes:
- `go-gateway.yaml` — Deployment + Service
- `go-gateway-hpa.yaml` — HorizontalPodAutoscaler (CPU-based)
- `go-gateway-monitor.yaml` — Prometheus ServiceMonitor
- `python-ai.yaml` — Deployment + Service
- `ollama-svc.yaml` — ExternalName service pointing to host Ollama

## Observability

### Metrics (Prometheus)

| Metric | Type | Description |
|---|---|---|
| `http_requests_total` | Counter | HTTP requests by path and status |
| `http_request_duration_seconds` | Histogram | HTTP response latency |
| `grpc_request_duration_seconds` | Histogram | gRPC call duration |
| `ai_generated_tokens_total` | Counter | LLM output tokens by model |
| `ai_generation_duration_seconds` | Histogram | LLM generation time |

### Distributed Tracing (Jaeger)

Both services instrument all requests with OpenTelemetry, propagating trace context via gRPC metadata. View full request traces at http://localhost:16686.

### Optional GPU Metrics Exporter

You can start the Python GPU exporter separately (outside Docker Compose):

```bash
./deploy.sh gpu
```

It exposes metrics at `http://localhost:9835/metrics`.

### Profiling (pprof)

Go gateway exposes runtime profiling at http://localhost:6060/debug/pprof — useful for CPU and memory analysis under load.

## Project Structure

```
.
├── proto/                  # Protobuf service definitions
│   ├── iris/v1/iris.proto
│   └── model/v1/model.proto
├── service_go/             # Go API gateway
│   ├── main.go
│   ├── Dockerfile
│   ├── go.mod
│   ├── handlers/           # HTTP handlers (iris/model/stream)
│   ├── router/             # Route wiring
│   ├── config/             # Env config
│   └── gen/                # Generated gRPC stubs
├── service_python/         # Python AI service
│   ├── main.py
│   ├── Dockerfile
│   ├── pyproject.toml
│   ├── models/             # Iris + Ollama predictors
│   ├── observability.py    # Logging + tracing setup
│   ├── server.py           # gRPC server wiring
│   └── gen/                # Generated gRPC stubs
├── test/
│   └── test.js             # k6 load test
├── docker-compose.yml
├── prometheus.yml
├── buf.gen.yaml            # Protobuf code generation config
└── deploy.sh               # K8s deployment helper
```

## Regenerating Protobuf Code

```bash
# Install buf: https://buf.build/docs/installation
buf generate
```
