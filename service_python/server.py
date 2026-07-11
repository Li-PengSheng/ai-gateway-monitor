# service_python/server.py
import logging
import signal
from concurrent import futures

import grpc

from iris.v1 import iris_pb2_grpc
from model.v1 import model_pb2_grpc

logger = logging.getLogger("python-ai")


MAX_WORKERS = 10

# The go-gateway client sends keepalive pings every GRPC_KEEP_ALIVE_TIME (30s)
# with PermitWithoutStream=true. gRPC servers reject such pings by default
# (min interval 5min, no pings without active calls) and answer with a
# GOAWAY "too_many_pings", causing constant reconnect churn.
SERVER_OPTIONS = [
    ("grpc.keepalive_permit_without_calls", 1),
    # Must be <= the client's keepalive Time; leave headroom for jitter.
    ("grpc.http2.min_ping_interval_without_data_ms", 20000),
    ("grpc.keepalive_time_ms", 60000),
    ("grpc.keepalive_timeout_ms", 10000),
]


def create_server(iris_predictor, model_predictor) -> grpc.Server:
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=SERVER_OPTIONS,
        # Reject excess load with RESOURCE_EXHAUSTED instead of queueing
        # unbounded work in the executor while callers time out.
        maximum_concurrent_rpcs=MAX_WORKERS * 2,
    )
    iris_pb2_grpc.add_IrisPredictorServicer_to_server(iris_predictor, server)
    model_pb2_grpc.add_ModelPredictorServicer_to_server(model_predictor, server)
    server.add_insecure_port("[::]:50051")
    logger.info("Python AI Service (gRPC) is running on port 50051...")
    return server


def setup_graceful_shutdown(server: grpc.Server, provider) -> None:
    def _shutdown(signum, frame):
        logger.info("Received signal %s, shutting down gracefully...", signum)
        server.stop(grace=5)
        provider.shutdown()
        logger.info("Server stopped.")

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)
