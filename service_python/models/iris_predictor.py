"""Iris flower classification gRPC servicer (scikit-learn RandomForest)."""

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
    """Implements ``iris.v1.IrisPredictor`` / ``IrisPredict``.

    Loads a pickled RandomForest when ``model_path`` exists; otherwise trains
    on the built-in sklearn iris dataset in memory (demo convenience, not a
    production model-distribution strategy — pickle also implies trust of the
    file source).
    """

    def __init__(self, model_path: Optional[str] = None):
        """Load or train the classifier and prepare iris metadata.

        Args:
            model_path: Path to a pickle artifact. If unset or missing on disk,
                trains ``RandomForestClassifier`` on ``load_iris()`` in-process.
        """
        self._iris_meta = load_iris()
        self._clf = self._load_model(model_path)
        logger.info(
            "IrisPredictor ready (model_path=%s)", model_path or "trained-in-memory"
        )

    def _load_model(self, model_path: Optional[str]) -> RandomForestClassifier:
        """Load a configured pickle artifact or train the demo fallback model.

        Args:
            model_path: Optional path to a trusted pickle artifact. A missing
                path is treated like no configuration and triggers fallback.

        Returns:
            The unpickled estimator, or a fitted ``RandomForestClassifier``.
            The annotation describes the expected artifact type; pickle loading
            does not enforce it at runtime.

        Raises:
            Exception: Propagated from opening or unpickling an existing artifact,
                or from fitting the fallback model. Invalid pickle data may raise
                ``UnpicklingError``, ``EOFError``, or an import-related exception.

        Side effects:
            Reads a trusted artifact from disk or trains a model in memory.
            Pickle may execute code while loading, so callers must not supply an
            artifact from an untrusted source. Falling back keeps local demos
            self-contained, but a misspelled path does not fail startup.
        """
        if model_path and os.path.exists(model_path):
            logger.info("Loading Iris model from %s", model_path)
            with open(model_path, "rb") as f:
                return pickle.load(f)

        logger.info("No model file found — training RandomForest in memory")
        clf = RandomForestClassifier()
        clf.fit(self._iris_meta.data, self._iris_meta.target)
        return clf

    def IrisPredict(self, request, context):
        """Classify one iris sample from four float features.

        Validation failures and prediction errors set gRPC status on ``context``
        and return an empty response (they are not raised to the framework).

        Args:
            request: ``IrisPredictRequest`` with sepal/petal lengths and widths.
            context: Servicer context; ``set_code`` / ``set_details`` on failure.

        Returns:
            ``IrisPredictResponse`` with ``class_id`` and ``class_name``, or empty
            when ``INVALID_ARGUMENT`` / ``INTERNAL`` was set on ``context``.
        """
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
