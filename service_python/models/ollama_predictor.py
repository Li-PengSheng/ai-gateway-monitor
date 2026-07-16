"""Ollama-backed LLM gRPC servicer (unary + server streaming)."""

import logging

import httpx
import ollama
from grpc import StatusCode

from model.v1 import model_pb2, model_pb2_grpc

logger = logging.getLogger("python-ai")


def _set_ollama_error(context, e: Exception) -> None:
    """Map Ollama/httpx failures to gRPC status on ``context`` (not re-raised).

    Gateway HTTP mapping: UNAVAILABLE→503, DEADLINE_EXCEEDED→504. Connection
    errors must not become INTERNAL or dependency outages look like 500s. All
    ``ollama.ResponseError`` values are intentionally collapsed to UNAVAILABLE:
    the public gateway contract currently exposes dependency availability, not
    Ollama-specific status codes such as a missing model. A finer mapping must
    be introduced together with the gateway API error contract.
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
    """Implements ``model.v1.ModelPredictor`` (unary + streaming).

    Calls Ollama over HTTP. Keep ``timeout_sec`` slightly below gateway
    ``MODEL_TIMEOUT`` so worker threads release after the client abandons the RPC.
    """

    def __init__(self, ollama_host: str, model_name: str, timeout_sec: float = 55.0):
        """Create an Ollama client bound to the configured model.

        Args:
            ollama_host: Ollama base URL (e.g. ``http://localhost:11434``).
            model_name: Model tag passed to ``generate`` (e.g. ``qwen2.5:1.5b``).
            timeout_sec: Per-call httpx timeout. Without an explicit value httpx
                waits forever and a hung Ollama pins a thread-pool worker after
                the gateway has already timed out.
        """
        self._client = ollama.Client(host=ollama_host, timeout=timeout_sec)
        self._model_name = model_name
        logger.info(
            "Ollama client initialized: host=%s model=%s timeout=%.0fs",
            ollama_host,
            model_name,
            timeout_sec,
        )

    def ModelPredict(self, request, context):
        """Run unary LLM generation for a single prompt.

        Logs prompt length only (not content — PII risk). ``num_predict=512``
        caps output tokens so a single call cannot run unbounded within the
        timeout budget. Failures set status on ``context``.

        Args:
            request: ``ModelPredictRequest``; prompt must be non-empty after strip.
            context: Servicer context; status set on validation or Ollama failure.

        Returns:
            Filled ``ModelPredictResponse``, or empty when an error status was set.
        """
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
        """Stream LLM tokens; yield one response per Ollama chunk.

        Checks ``context.is_active()`` after each received chunk so client
        cancellation or a deadline stops further consumption. Cancellation does
        not interrupt a worker already blocked waiting for the next Ollama chunk,
        and returning does not explicitly send an Ollama cancellation request;
        the backend may continue until its HTTP stream closes or its own timeout
        expires. Eval stats are typically only on the final chunk (ollama>=0.4
        ``GenerateResponse`` objects, not dicts).

        Args:
            request: ``ModelPredictRequest``; prompt must be non-empty after strip.
            context: Servicer context; cancellation is observed between chunks.

        Yields:
            ``ModelPredictResponse`` chunks from Ollama. Failures set gRPC status
            via ``_set_ollama_error`` and end the stream without raising.
        """
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
                if not context.is_active():
                    logger.info("Client cancelled stream, stopping Ollama read")
                    return
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
