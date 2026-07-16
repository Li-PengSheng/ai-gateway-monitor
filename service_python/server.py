"""gRPC server factory and SIGTERM/SIGINT shutdown for python-ai."""

import logging
import signal
from concurrent import futures

import grpc

from iris.v1 import iris_pb2_grpc
from model.v1 import model_pb2_grpc

logger = logging.getLogger("python-ai")


# Ollama calls are blocking, so each active inference occupies one worker.
# Ten workers bound thread and connection use for this demo-sized backend;
# maximum_concurrent_rpcs allows one additional worker-sized batch to wait in
# gRPC before rejecting further calls instead of growing an unbounded queue.
MAX_WORKERS = 10

# The go-gateway client sends keepalive pings every GRPC_KEEP_ALIVE_TIME (30s)
# with PermitWithoutStream=true. gRPC servers reject such pings by default
# (min interval 5min, no pings without active calls) and answer with a
# GOAWAY "too_many_pings", causing constant reconnect churn.
SERVER_OPTIONS = [
    ("grpc.keepalive_permit_without_calls", 1),
    # Server min ping interval (20s) must be below the client ping period (30s)
    # so keepalive pings are accepted; keep paired with go-gateway config.
    ("grpc.http2.min_ping_interval_without_data_ms", 20000),
    ("grpc.keepalive_time_ms", 60000),
    ("grpc.keepalive_timeout_ms", 10000),
]


def create_server(iris_predictor, model_predictor) -> grpc.Server:
    """Build an insecure gRPC server on ``[::]:50051`` with both servicers.

    ``maximum_concurrent_rpcs`` is ``MAX_WORKERS * 2``: at most ``MAX_WORKERS``
    blocking calls execute while one equally sized batch may wait for a thread.
    Further load receives RESOURCE_EXHAUSTED instead of queueing unboundedly
    until gateway deadlines expire. Keep ``SERVER_OPTIONS`` paired with the
    go-gateway ``GRPC_KEEP_ALIVE_TIME``.

    Args:
        iris_predictor: ``IrisPredictor`` servicer instance.
        model_predictor: ``ModelPredictor`` servicer instance.

    Returns:
        Configured server; call ``start()`` before accepting RPCs.
    """
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=SERVER_OPTIONS,
        maximum_concurrent_rpcs=MAX_WORKERS * 2,
    )
    iris_pb2_grpc.add_IrisPredictorServicer_to_server(iris_predictor, server)
    model_pb2_grpc.add_ModelPredictorServicer_to_server(model_predictor, server)
    server.add_insecure_port("[::]:50051")
    # Port registered only; listen begins in main after server.start().
    logger.info("Python AI Service (gRPC) is running on port 50051...")
    return server


def setup_graceful_shutdown(server: grpc.Server, provider) -> None:
    """Register SIGTERM/SIGINT handlers that stop gRPC and the tracer provider.

    On signal, ``server.stop(grace=5)`` initiates graceful gRPC shutdown and
    returns immediately; this handler does not wait on the returned Event.
    ``provider.shutdown()`` therefore flushes tracing while active RPCs may
    still be using their five-second grace period. The grace period remains
    shorter than the gateway and pod shutdown windows so kubelet retains
    termination headroom.

    Args:
        server: Server from ``create_server``.
        provider: Object with ``shutdown()`` (typically the OTel TracerProvider).

    Side effects:
        Replaces the process handlers for SIGTERM and SIGINT. When invoked, a
        handler starts server shutdown and flushes the tracing provider.
    """

    def _shutdown(signum, frame):
        logger.info("Received signal %s, shutting down gracefully...", signum)
        server.stop(grace=5)
        provider.shutdown()
        logger.info("Server stopped.")

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)
