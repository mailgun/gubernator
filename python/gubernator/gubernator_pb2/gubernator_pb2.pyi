from google.api import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

BATCHING: Behavior
DESCRIPTOR: _descriptor.FileDescriptor
DURATION_IS_GREGORIAN: Behavior
GLOBAL: Behavior
LEAKY_BUCKET: Algorithm
MULTI_REGION: Behavior
NO_BATCHING: Behavior
OVER_LIMIT: Status
RESET_REMAINING: Behavior
TOKEN_BUCKET: Algorithm
UNDER_LIMIT: Status

class GetRateLimitsReq(_message.Message):
    __slots__ = ["requests"]
    REQUESTS_FIELD_NUMBER: _ClassVar[int]
    requests: _containers.RepeatedCompositeFieldContainer[RateLimitReq]
    def __init__(self, requests: _Optional[_Iterable[_Union[RateLimitReq, _Mapping]]] = ...) -> None: ...

class GetRateLimitsResp(_message.Message):
    __slots__ = ["responses"]
    RESPONSES_FIELD_NUMBER: _ClassVar[int]
    responses: _containers.RepeatedCompositeFieldContainer[RateLimitResp]
    def __init__(self, responses: _Optional[_Iterable[_Union[RateLimitResp, _Mapping]]] = ...) -> None: ...

class HealthCheckReq(_message.Message):
    __slots__ = []
    def __init__(self) -> None: ...

class HealthCheckResp(_message.Message):
    __slots__ = ["message", "peer_count", "status"]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    PEER_COUNT_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    message: str
    peer_count: int
    status: str
    def __init__(self, status: _Optional[str] = ..., message: _Optional[str] = ..., peer_count: _Optional[int] = ...) -> None: ...

class RateLimitReq(_message.Message):
    __slots__ = ["algorithm", "behavior", "burst", "duration", "hits", "limit", "name", "unique_key"]
    ALGORITHM_FIELD_NUMBER: _ClassVar[int]
    BEHAVIOR_FIELD_NUMBER: _ClassVar[int]
    BURST_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    HITS_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    UNIQUE_KEY_FIELD_NUMBER: _ClassVar[int]
    algorithm: Algorithm
    behavior: Behavior
    burst: int
    duration: int
    hits: int
    limit: int
    name: str
    unique_key: str
    def __init__(self, name: _Optional[str] = ..., unique_key: _Optional[str] = ..., hits: _Optional[int] = ..., limit: _Optional[int] = ..., duration: _Optional[int] = ..., algorithm: _Optional[_Union[Algorithm, str]] = ..., behavior: _Optional[_Union[Behavior, str]] = ..., burst: _Optional[int] = ...) -> None: ...

class RateLimitResp(_message.Message):
    __slots__ = ["error", "limit", "metadata", "remaining", "reset_time", "status"]
    class MetadataEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    ERROR_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    REMAINING_FIELD_NUMBER: _ClassVar[int]
    RESET_TIME_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    error: str
    limit: int
    metadata: _containers.ScalarMap[str, str]
    remaining: int
    reset_time: int
    status: Status
    def __init__(self, status: _Optional[_Union[Status, str]] = ..., limit: _Optional[int] = ..., remaining: _Optional[int] = ..., reset_time: _Optional[int] = ..., error: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class Algorithm(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []

class Behavior(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []

class Status(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
