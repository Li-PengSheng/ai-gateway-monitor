from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class ModelPredictRequest(_message.Message):
    __slots__ = ("prompt",)
    PROMPT_FIELD_NUMBER: _ClassVar[int]
    prompt: str
    def __init__(self, prompt: _Optional[str] = ...) -> None: ...

class ModelPredictResponse(_message.Message):
    __slots__ = (
        "response",
        "model_name",
        "prompt_eval_count",
        "eval_count",
        "eval_duration",
    )
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    PROMPT_EVAL_COUNT_FIELD_NUMBER: _ClassVar[int]
    EVAL_COUNT_FIELD_NUMBER: _ClassVar[int]
    EVAL_DURATION_FIELD_NUMBER: _ClassVar[int]
    response: str
    model_name: str
    prompt_eval_count: int
    eval_count: int
    eval_duration: int
    def __init__(
        self,
        response: _Optional[str] = ...,
        model_name: _Optional[str] = ...,
        prompt_eval_count: _Optional[int] = ...,
        eval_count: _Optional[int] = ...,
        eval_duration: _Optional[int] = ...,
    ) -> None: ...
