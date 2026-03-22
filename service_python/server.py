# service_python/server.py
"""def create_server(iris_predictor, model_predictor):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    iris_pb2_grpc.add_IrisPredictorServicer_to_server(iris_predictor, server)
    model_pb2_grpc.add_ModelPredictorServicer_to_server(model_predictor, server)
    server.add_insecure_port("[::]:50051")
    logger.info("Python AI Service (gRPC) is running on port 50051...")
    return server

def setup_graceful_shutdown(server):
    def _shutdown(signum, frame):
        logger.info("Received signal %s, shutting down...", signum)
        server.stop(grace=5)
        logger.info("Server stopped.")
    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)"""

import logging
import signal
from concurrent import futures

import grpc

from iris.v1 import iris_pb2_grpc
from model.v1 import model_pb2_grpc

logger = logging.getLogger("python-ai")


def create_server(iris_predictor, model_predictor) -> grpc.Server:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
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
