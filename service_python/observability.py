# service_python/observability.py
import logging
import os
import sys

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.grpc import GrpcInstrumentorServer
from opentelemetry.propagate import set_global_textmap
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator


def _resolve_log_level() -> int:
    raw = os.getenv("LOG_LEVEL", "INFO")
    levels = {
        "DEBUG": logging.DEBUG,
        "INFO": logging.INFO,
        "WARNING": logging.WARNING,
        "ERROR": logging.ERROR,
    }
    level = levels.get(raw.upper())
    if level is None:
        print(
            f"WARNING: unrecognized LOG_LEVEL={raw!r}, falling back to INFO",
            file=sys.stderr,
        )
        return logging.INFO
    return level


def setup_logging() -> logging.Logger:
    logging.basicConfig(
        level=_resolve_log_level(),
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%Y-%m-%dT%H:%M:%S",
    )
    return logging.getLogger("python-ai")


def setup_tracing() -> TracerProvider:
    resource = Resource.create({"service.name": "python-ai"})
    provider = TracerProvider(resource=resource)

    jaeger_endpoint = os.getenv("JAEGER_ENDPOINT", "localhost:4317")
    processor = BatchSpanProcessor(
        OTLPSpanExporter(endpoint=jaeger_endpoint, insecure=True)
    )
    provider.add_span_processor(processor)
    trace.set_tracer_provider(provider)

    set_global_textmap(TraceContextTextMapPropagator())

    GrpcInstrumentorServer().instrument()

    return provider
