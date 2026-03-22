from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class IrisPredictRequest(_message.Message):
    __slots__ = ("sepal_length", "sepal_width", "petal_length", "petal_width")
    SEPAL_LENGTH_FIELD_NUMBER: _ClassVar[int]
    SEPAL_WIDTH_FIELD_NUMBER: _ClassVar[int]
    PETAL_LENGTH_FIELD_NUMBER: _ClassVar[int]
    PETAL_WIDTH_FIELD_NUMBER: _ClassVar[int]
    sepal_length: float
    sepal_width: float
    petal_length: float
    petal_width: float
    def __init__(
        self,
        sepal_length: _Optional[float] = ...,
        sepal_width: _Optional[float] = ...,
        petal_length: _Optional[float] = ...,
        petal_width: _Optional[float] = ...,
    ) -> None: ...

class IrisPredictResponse(_message.Message):
    __slots__ = ("class_id", "class_name")
    CLASS_ID_FIELD_NUMBER: _ClassVar[int]
    CLASS_NAME_FIELD_NUMBER: _ClassVar[int]
    class_id: int
    class_name: str
    def __init__(
        self, class_id: _Optional[int] = ..., class_name: _Optional[str] = ...
    ) -> None: ...
