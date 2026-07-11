import os
import sys

sys.path.append(os.path.join(os.path.dirname(__file__), "gen"))

from models import IrisPredictor, ModelPredictor
from observability import setup_logging, setup_tracing
from server import create_server, setup_graceful_shutdown

if __name__ == "__main__":
    logger = setup_logging()
    provider = setup_tracing()

    iris_predictor = IrisPredictor(model_path=os.getenv("IRIS_MODEL_PATH"))
    model_predictor = ModelPredictor(
        ollama_host=os.getenv("OLLAMA_HOST", "http://localhost:11434"),
        model_name=os.getenv("MODEL_NAME", "qwen2.5:1.5b"),
        # Slightly below the gateway's MODEL_TIMEOUT (60s) so the worker thread
        # is released even when the caller has already given up.
        timeout_sec=float(os.getenv("OLLAMA_TIMEOUT_SEC", "55")),
    )

    server = create_server(iris_predictor, model_predictor)
    setup_graceful_shutdown(server, provider)

    server.start()
    logger.info("Server started. Waiting for termination...")
    server.wait_for_termination()
