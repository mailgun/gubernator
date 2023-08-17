from google.api import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Algorithm(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    TOKEN_BUCKET: _ClassVar[Algorithm]
    LEAKY_BUCKET: _ClassVar[Algorithm]

class Behavior(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    BATCHING: _ClassVar[Behavior]
    NO_BATCHING: _ClassVar[Behavior]
    GLOBAL: _ClassVar[Behavior]
    DURATION_IS_GREGORIAN: _ClassVar[Behavior]
    RESET_REMAINING: _ClassVar[Behavior]
    MULTI_REGION: _ClassVar[Behavior]

class Status(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = []
    UNDER_LIMIT: _ClassVar[Status]
    OVER_LIMIT: _ClassVar[Status]
TOKEN_BUCKET: Algorithm
LEAKY_BUCKET: Algorithm
BATCHING: Behavior
NO_BATCHING: Behavior
GLOBAL: Behavior
DURATION_IS_GREGORIAN: Behavior
RESET_REMAINING: Behavior
MULTI_REGION: Behavior
UNDER_LIMIT: Status
OVER_LIMIT: Status

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

class RateLimitReq(_message.Message):
    __slots__ = ["name", "unique_key", "hits", "limit", "duration", "algorithm", "behavior", "burst", "metadata"]
    class MetadataEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    NAME_FIELD_NUMBER: _ClassVar[int]
    UNIQUE_KEY_FIELD_NUMBER: _ClassVar[int]
    HITS_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    ALGORITHM_FIELD_NUMBER: _ClassVar[int]
    BEHAVIOR_FIELD_NUMBER: _ClassVar[int]
    BURST_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    name: str
    unique_key: str
    hits: int
    limit: int
    duration: int
    algorithm: Algorithm
    behavior: Behavior
    burst: int
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, name: _Optional[str] = ..., unique_key: _Optional[str] = ..., hits: _Optional[int] = ..., limit: _Optional[int] = ..., duration: _Optional[int] = ..., algorithm: _Optional[_Union[Algorithm, str]] = ..., behavior: _Optional[_Union[Behavior, str]] = ..., burst: _Optional[int] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class RateLimitResp(_message.Message):
    __slots__ = ["status", "limit", "remaining", "reset_time", "error", "metadata"]
    class MetadataEntry(_message.Message):
        __slots__ = ["key", "value"]
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    STATUS_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    REMAINING_FIELD_NUMBER: _ClassVar[int]
    RESET_TIME_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    status: Status
    limit: int
    remaining: int
    reset_time: int
    error: str
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, status: _Optional[_Union[Status, str]] = ..., limit: _Optional[int] = ..., remaining: _Optional[int] = ..., reset_time: _Optional[int] = ..., error: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class HealthCheckReq(_message.Message):
    __slots__ = []
    def __init__(self) -> None: ...

class HealthCheckResp(_message.Message):
    __slots__ = ["status", "message", "peer_count"]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    PEER_COUNT_FIELD_NUMBER: _ClassVar[int]
    status: str
    message: str
    peer_count: int
    def __init__(self, status: _Optional[str] = ..., message: _Optional[str] = ..., peer_count: _Optional[int] = ...) -> None: ...
