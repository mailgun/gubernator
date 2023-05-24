from gubernator import gubernator_pb2 as _gubernator_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GetPeerRateLimitsReq(_message.Message):
    __slots__ = ["requests"]
    REQUESTS_FIELD_NUMBER: _ClassVar[int]
    requests: _containers.RepeatedCompositeFieldContainer[_gubernator_pb2.RateLimitReq]
    def __init__(self, requests: _Optional[_Iterable[_Union[_gubernator_pb2.RateLimitReq, _Mapping]]] = ...) -> None: ...

class GetPeerRateLimitsResp(_message.Message):
    __slots__ = ["rate_limits"]
    RATE_LIMITS_FIELD_NUMBER: _ClassVar[int]
    rate_limits: _containers.RepeatedCompositeFieldContainer[_gubernator_pb2.RateLimitResp]
    def __init__(self, rate_limits: _Optional[_Iterable[_Union[_gubernator_pb2.RateLimitResp, _Mapping]]] = ...) -> None: ...

class UpdatePeerGlobalsReq(_message.Message):
    __slots__ = ["globals"]
    GLOBALS_FIELD_NUMBER: _ClassVar[int]
    globals: _containers.RepeatedCompositeFieldContainer[UpdatePeerGlobal]
    def __init__(self, globals: _Optional[_Iterable[_Union[UpdatePeerGlobal, _Mapping]]] = ...) -> None: ...

class UpdatePeerGlobal(_message.Message):
    __slots__ = ["key", "status", "algorithm"]
    KEY_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    ALGORITHM_FIELD_NUMBER: _ClassVar[int]
    key: str
    status: _gubernator_pb2.RateLimitResp
    algorithm: _gubernator_pb2.Algorithm
    def __init__(self, key: _Optional[str] = ..., status: _Optional[_Union[_gubernator_pb2.RateLimitResp, _Mapping]] = ..., algorithm: _Optional[_Union[_gubernator_pb2.Algorithm, str]] = ...) -> None: ...

class UpdatePeerGlobalsResp(_message.Message):
    __slots__ = []
    def __init__(self) -> None: ...
