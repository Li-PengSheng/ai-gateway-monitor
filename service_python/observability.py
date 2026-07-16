"""Structured logging and OpenTelemetry setup for the python-ai gRPC service."""

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
    """Resolve ``LOG_LEVEL`` to a logging constant, defaulting invalid values.

    Returns:
        A standard-library logging level. Missing values resolve to INFO.

    Side effects:
        Writes a warning to stderr when the configured value is unsupported;
        logging is not configured yet, so the warning cannot use a logger.
    """
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
    """Configure root logging from ``LOG_LEVEL`` and return the service logger.

    Returns:
        The ``python-ai`` logger used by servicers and lifecycle code.

    Side effects:
        Configures the process root logger through ``logging.basicConfig``.
        Calls after logging is already configured may leave existing handlers
        unchanged, following standard-library behavior.
    """
    logging.basicConfig(
        level=_resolve_log_level(),
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%Y-%m-%dT%H:%M:%S",
    )
    return logging.getLogger("python-ai")


def setup_tracing() -> TracerProvider:
    """Install a global TracerProvider exporting OTLP/gRPC to ``JAEGER_ENDPOINT``.

    Also sets the TraceContext propagator and auto-instruments inbound gRPC
    RPCs (this service has no HTTP server). Caller must ``shutdown()`` the
    provider on process exit.

    Returns:
        Provider used for span export; pass to ``setup_graceful_shutdown``.

    Raises:
        Exception: Propagated if exporter construction or instrumentation setup
            fails. Collector unavailability after setup is handled asynchronously
            by the batch exporter and is not reported by this return path.
    """
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
