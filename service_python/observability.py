# service_python/observability.py
"""def setup_logging():
    # 配置结构化日志
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%Y-%m-%dT%H:%M:%S",
    )
    logger = logging.getLogger("python-ai")
    return logger

def setup_tracing():
    # Initialize OpenTelemetry provider
    # 1. 初始化追踪器，发送到 Jaeger
    resource = Resource.create({"service.name": "python-ai"})
    provider = TracerProvider(resource=resource)
    jaeger_endpoint = os.getenv("JAEGER_ENDPOINT", "localhost:4317")
    processor = BatchSpanProcessor(OTLPSpanExporter(endpoint=jaeger_endpoint, insecure=True))
    # processor = BatchSpanProcessor(OTLPSpanExporter(endpoint="jaeger:4317", insecure=True))
    provider.add_span_processor(processor)
    trace.set_tracer_provider(provider)

    # 设置全局传播器，用于跨服务追踪上下文传递
    set_global_textmap(TraceContextTextMapPropagator())

    # 2. 自动拦截所有 gRPC 请求
    instrumentor = GrpcInstrumentorServer()
    instrumentor.instrument()"""

import logging
import os

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.grpc import GrpcInstrumentorServer
from opentelemetry.propagate import set_global_textmap
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator


def setup_logging() -> logging.Logger:
    logging.basicConfig(
        level=logging.INFO,
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
