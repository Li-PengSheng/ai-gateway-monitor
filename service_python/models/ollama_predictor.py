# service_python/models/ollama_predictor.py
"""class ModelPredictor(model_pb2_grpc.ModelPredictorServicer):
def __init__(self, ollama_host: str, model_name: str):
    self.client = ollama.Client(host=ollama_host)
    self.model_name = model_name
    logger.info("Ollama client initialized: host=%s model=%s", ollama_host, model_name)

def ModelPredict(self, request, context):
    logger.info("Model predict request: prompt='%.80s...'", request.prompt)
    try:
        response = self.client.generate(
            model=self.model_name,
            prompt=request.prompt,
            options={"num_predict": 512},  # 限制最大输出 token
        )
    except ollama.ResponseError as e:
        logger.error("Ollama response error: %s", e)
        context.set_code(StatusCode.UNAVAILABLE)
        context.set_details(f"Model backend error: {e}")
        return model_pb2.ModelPredictResponse()
    except Exception as e:
        logger.error("Unexpected error calling Ollama: %s", e)
        context.set_code(StatusCode.INTERNAL)
        context.set_details("Internal model error")
        return model_pb2.ModelPredictResponse()

    logger.info(
        "Model response: tokens_in=%s tokens_out=%s",
        response.prompt_eval_count,
        response.eval_count,
    )
    return model_pb2.ModelPredictResponse(
        response=response.response,
        model_name=self.model_name,
        prompt_eval_count=response.prompt_eval_count or 0,
        eval_count=response.eval_count or 0,
        eval_duration=response.eval_duration or 0,
    )"""

import logging

import ollama
from grpc import StatusCode

from model.v1 import model_pb2, model_pb2_grpc

logger = logging.getLogger("python-ai")


class ModelPredictor(model_pb2_grpc.ModelPredictorServicer):
    def __init__(self, ollama_host: str, model_name: str):
        self._client = ollama.Client(host=ollama_host)
        self._model_name = model_name
        logger.info(
            "Ollama client initialized: host=%s model=%s", ollama_host, model_name
        )

    def ModelPredict(self, request, context):
        logger.info("Model predict request: prompt='%.80s...'", request.prompt)
        try:
            response = self._client.generate(
                model=self._model_name,
                prompt=request.prompt,
                options={"num_predict": 512},
            )
        except ollama.ResponseError as e:
            logger.error("Ollama response error: %s", e)
            context.set_code(StatusCode.UNAVAILABLE)
            context.set_details(f"Model backend error: {e}")
            return model_pb2.ModelPredictResponse()
        except Exception as e:
            logger.error("Unexpected error calling Ollama: %s", e)
            context.set_code(StatusCode.INTERNAL)
            context.set_details("Internal model error")
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
