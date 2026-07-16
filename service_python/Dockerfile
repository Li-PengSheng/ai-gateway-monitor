# Slim Python runtime for the python-ai gRPC service
FROM python:3.12-slim

COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

RUN useradd --create-home --uid 1000 appuser

WORKDIR /app

COPY pyproject.toml uv.lock ./
RUN uv sync --frozen && chown -R appuser:appuser /app

COPY --chown=appuser:appuser gen/ ./gen/
COPY --chown=appuser:appuser models/ ./models/
COPY --chown=appuser:appuser main.py observability.py server.py gpu_exporter.py ./

USER appuser

# gen/ must be on PYTHONPATH so "from iris.v1 import ..." resolves
ENV PYTHONPATH="/app:/app/gen"

EXPOSE 50051

CMD ["uv", "run", "main.py"]
