# service_python/models/ollama_predictor.py
import logging

import httpx
import ollama
from grpc import StatusCode

from model.v1 import model_pb2, model_pb2_grpc

logger = logging.getLogger("python-ai")


def _set_ollama_error(context, e: Exception) -> None:
    """Map Ollama/transport failures to meaningful gRPC status codes.

    The gateway translates these to HTTP: UNAVAILABLE -> 503,
    DEADLINE_EXCEEDED -> 504, INVALID_ARGUMENT -> 400. Lumping connection
    errors into INTERNAL would surface dependency outages as 500s.
    """
    if isinstance(e, httpx.TimeoutException):
        context.set_code(StatusCode.DEADLINE_EXCEEDED)
        context.set_details("Model backend timed out")
    elif isinstance(e, (httpx.TransportError, ollama.ResponseError)):
        context.set_code(StatusCode.UNAVAILABLE)
        context.set_details(f"Model backend unavailable: {e}")
    else:
        context.set_code(StatusCode.INTERNAL)
        context.set_details("Internal model error")


class ModelPredictor(model_pb2_grpc.ModelPredictorServicer):
    def __init__(self, ollama_host: str, model_name: str, timeout_sec: float = 55.0):
        # Without an explicit timeout httpx waits forever; a hung Ollama call
        # would then pin a ThreadPoolExecutor worker long after the gateway
        # (MODEL_TIMEOUT, default 60s) has given up on the RPC.
        self._client = ollama.Client(host=ollama_host, timeout=timeout_sec)
        self._model_name = model_name
        logger.info(
            "Ollama client initialized: host=%s model=%s timeout=%.0fs",
            ollama_host,
            model_name,
            timeout_sec,
        )

    def ModelPredict(self, request, context):
        # Log only the prompt length: prompt content is user input (PII risk).
        logger.info("Model predict request: prompt_len=%d", len(request.prompt))
        if not request.prompt.strip():
            context.set_code(StatusCode.INVALID_ARGUMENT)
            context.set_details("prompt must not be empty")
            return model_pb2.ModelPredictResponse()
        try:
            response = self._client.generate(
                model=self._model_name,
                prompt=request.prompt,
                options={"num_predict": 512},
            )
        except Exception as e:
            logger.error("Ollama call failed: %s: %s", type(e).__name__, e)
            _set_ollama_error(context, e)
            return model_pb2.ModelPredictResponse()

        logger.info(
            "Model response: tokens_in=%s tokens_out=%s",
            response.prompt_eval_count,
            response.eval_count,
        )
        return model_pb2.ModelPredictResponse(
            response=response.response,
            model_name=self._model_name,
            prompt_eval_count=response.prompt_eval_count or 0,
            eval_count=response.eval_count or 0,
            eval_duration=response.eval_duration or 0,
        )

    def ModelPredictStream(self, request, context):
        logger.info("Model predict stream request: prompt_len=%d", len(request.prompt))
        if not request.prompt.strip():
            context.set_code(StatusCode.INVALID_ARGUMENT)
            context.set_details("prompt must not be empty")
            return
        try:
            stream = self._client.generate(
                model=self._model_name,
                prompt=request.prompt,
                options={"num_predict": 512},
                stream=True,
            )
        except Exception as e:
            logger.error("Ollama call failed: %s: %s", type(e).__name__, e)
            _set_ollama_error(context, e)
            return

        try:
            for chunk in stream:
                # Client cancelled (or deadline hit) — stop pulling from Ollama.
                if not context.is_active():
                    logger.info("Client cancelled stream, stopping Ollama read")
                    return
                # ollama>=0.4 yields GenerateResponse objects, not dicts.
                # Only the final chunk has eval stats populated.
                yield model_pb2.ModelPredictResponse(
                    response=chunk.response or "",
                    model_name=self._model_name,
                    prompt_eval_count=chunk.prompt_eval_count or 0,
                    eval_count=chunk.eval_count or 0,
                    eval_duration=chunk.eval_duration or 0,
                )
        except Exception as e:
            logger.error("Error during stream: %s: %s", type(e).__name__, e)
            _set_ollama_error(context, e)
