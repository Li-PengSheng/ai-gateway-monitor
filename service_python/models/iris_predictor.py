# service_python/models/iris_predictor.py
"""class IrisPredictor(iris_pb2_grpc.IrisPredictorServicer):
def __init__(self, model_path: str = None):
    self.model = self._load_model(model_path)

def IrisPredict(self, request, context):
    # Prediction logic
    logger.info(
        "Iris predict request: sepal_len=%.2f sepal_wid=%.2f petal_len=%.2f petal_wid=%.2f",
        request.sepal_length,
        request.sepal_width,
        request.petal_length,
        request.petal_width,
    )

    features = [
        [
            request.sepal_length,
            request.sepal_width,
            request.petal_length,
            request.petal_width,
        ]
    ]

    pred_idx = clf.predict(features)[0]
    class_name = iris.target_names[pred_idx]

    return iris_pb2.IrisPredictResponse(class_id=pred_idx, class_name=class_name)
"""

import logging
import os
import pickle
from typing import Optional

from sklearn.datasets import load_iris
from sklearn.ensemble import RandomForestClassifier

from iris.v1 import iris_pb2, iris_pb2_grpc

logger = logging.getLogger("python-ai")


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
        logger.info(
            "Iris predict request: sepal_len=%.2f sepal_wid=%.2f petal_len=%.2f petal_wid=%.2f",
            request.sepal_length,
            request.sepal_width,
            request.petal_length,
            request.petal_width,
        )
        features = [
            [
                request.sepal_length,
                request.sepal_width,
                request.petal_length,
                request.petal_width,
            ]
        ]
        pred_idx = self._clf.predict(features)[0]
        class_name = self._iris_meta.target_names[pred_idx]
        return iris_pb2.IrisPredictResponse(class_id=pred_idx, class_name=class_name)
