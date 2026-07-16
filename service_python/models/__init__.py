"""Public predictors exported by the models package."""

from .iris_predictor import IrisPredictor
from .ollama_predictor import ModelPredictor

__all__ = ["IrisPredictor", "ModelPredictor"]
