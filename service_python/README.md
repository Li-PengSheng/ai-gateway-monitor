# python-ai Service

The Python AI service is a gRPC backend that provides two inference endpoints:

- **IrisPredictor** — classifies Iris flower species using a scikit-learn RandomForest model.
- **ModelPredictor** — performs unary and streaming LLM inference via a locally running [Ollama](https://ollama.com/) instance.

## Package Structure

```
service_python/
├── main.py              # Entry point: wires logging, tracing, and gRPC server
├── server.py            # gRPC server creation and graceful shutdown
├── observability.py     # Structured logging and OpenTelemetry tracing setup
├── gpu_exporter.py      # Optional Prometheus GPU metrics exporter (nvidia-smi)
├── models/
│   ├── iris_predictor.py    # IrisPredictor gRPC servicer
│   └── ollama_predictor.py  # ModelPredictor gRPC servicer (unary + streaming)
├── gen/                 # Auto-generated gRPC stubs (do not edit)
├── Dockerfile
└── pyproject.toml
```

## gRPC Endpoints

| Method | RPC Type | Description |
|---|---|---|
| `IrisPredictor.IrisPredict` | Unary | Predicts Iris species from four float features |
| `ModelPredictor.ModelPredict` | Unary | Generates a full LLM response for a given prompt |
| `ModelPredictor.ModelPredictStream` | Server streaming | Streams LLM tokens as they are generated |

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama API base URL |
| `MODEL_NAME` | `qwen2.5:1.5b` | Ollama model to use for inference |
| `IRIS_MODEL_PATH` | _(unset)_ | Path to a pre-trained Iris pickle file; trains in memory if unset |
| `JAEGER_ENDPOINT` | `localhost:4317` | OTLP gRPC endpoint for distributed trace export |

## Running Locally

```bash
# Install dependencies (requires uv)
cd service_python
uv sync

# Start Ollama with the required model first
ollama pull qwen2.5:1.5b

# Run the gRPC server
uv run python main.py
```

The service listens on `[::]:50051`.

## GPU Metrics Exporter (Optional)

A standalone Prometheus exporter that scrapes `nvidia-smi` and exposes GPU
utilization, memory usage, and temperature metrics on port `9835`.

```bash
uv run python gpu_exporter.py
```

Metrics endpoint: `http://localhost:9835/metrics`
