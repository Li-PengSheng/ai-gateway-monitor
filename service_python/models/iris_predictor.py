# service_python/models/iris_predictor.py
import logging
import math
import os
import pickle
from typing import Optional

from grpc import StatusCode
from sklearn.datasets import load_iris
from sklearn.ensemble import RandomForestClassifier

from iris.v1 import iris_pb2, iris_pb2_grpc

logger = logging.getLogger("python-ai")

# Iris measurements are a few cm; reject anything wildly out of range instead
# of feeding NaN/Inf/garbage into sklearn (which would raise -> gRPC UNKNOWN).
MAX_FEATURE_CM = 50.0


class IrisPredictor(iris_pb2_grpc.IrisPredictorServicer):
    def __init__(self, model_path: Optional[str] = None):
        self._iris_meta = load_iris()
        self._clf = self._load_model(model_path)
        logger.info(
            "IrisPredictor ready (model_path=%s)", model_path or "trained-in-memory"
        )

    def _load_model(self, model_path: Optional[str]) -> RandomForestClassifier:
        if model_path and os.path.exists(model_path):
            logger.info("Loading Iris model from %s", model_path)
            with open(model_path, "rb") as f:
                return pickle.load(f)

        logger.info("No model file found — training RandomForest in memory")
        clf = RandomForestClassifier()
        clf.fit(self._iris_meta.data, self._iris_meta.target)
        return clf

    def IrisPredict(self, request, context):
        logger.debug(
            "Iris predict request: sepal_len=%.2f sepal_wid=%.2f petal_len=%.2f petal_wid=%.2f",
            request.sepal_length,
            request.sepal_width,
            request.petal_length,
            request.petal_width,
        )
        features = {
            "sepal_length": request.sepal_length,
            "sepal_width": request.sepal_width,
            "petal_length": request.petal_length,
            "petal_width": request.petal_width,
        }
        for name, value in features.items():
            if not math.isfinite(value) or value < 0 or value > MAX_FEATURE_CM:
                context.set_code(StatusCode.INVALID_ARGUMENT)
                context.set_details(
                    f"{name} must be a finite number between 0 and {MAX_FEATURE_CM:g}"
                )
                return iris_pb2.IrisPredictResponse()

        try:
            pred_idx = self._clf.predict([list(features.values())])[0]
            class_name = self._iris_meta.target_names[pred_idx]
        except Exception as e:
            logger.error("Iris prediction failed: %s: %s", type(e).__name__, e)
            context.set_code(StatusCode.INTERNAL)
            context.set_details("Iris prediction failed")
            return iris_pb2.IrisPredictResponse()

        return iris_pb2.IrisPredictResponse(class_id=pred_idx, class_name=class_name)
