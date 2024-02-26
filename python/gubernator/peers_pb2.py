# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: peers.proto
# Protobuf Python Version: 4.25.3
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


import gubernator_pb2 as gubernator__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x0bpeers.proto\x12\rpb.gubernator\x1a\x10gubernator.proto\"O\n\x14GetPeerRateLimitsReq\x12\x37\n\x08requests\x18\x01 \x03(\x0b\x32\x1b.pb.gubernator.RateLimitReqR\x08requests\"V\n\x15GetPeerRateLimitsResp\x12=\n\x0brate_limits\x18\x01 \x03(\x0b\x32\x1c.pb.gubernator.RateLimitRespR\nrateLimits\"Q\n\x14UpdatePeerGlobalsReq\x12\x39\n\x07globals\x18\x01 \x03(\x0b\x32\x1f.pb.gubernator.UpdatePeerGlobalR\x07globals\"\xd1\x01\n\x10UpdatePeerGlobal\x12\x10\n\x03key\x18\x01 \x01(\tR\x03key\x12\x34\n\x06status\x18\x02 \x01(\x0b\x32\x1c.pb.gubernator.RateLimitRespR\x06status\x12\x36\n\talgorithm\x18\x03 \x01(\x0e\x32\x18.pb.gubernator.AlgorithmR\talgorithm\x12\x1a\n\x08\x64uration\x18\x04 \x01(\x03R\x08\x64uration\x12!\n\x0crequest_time\x18\x05 \x01(\x03R\x0brequestTime\"\x17\n\x15UpdatePeerGlobalsResp2\xcd\x01\n\x07PeersV1\x12`\n\x11GetPeerRateLimits\x12#.pb.gubernator.GetPeerRateLimitsReq\x1a$.pb.gubernator.GetPeerRateLimitsResp\"\x00\x12`\n\x11UpdatePeerGlobals\x12#.pb.gubernator.UpdatePeerGlobalsReq\x1a$.pb.gubernator.UpdatePeerGlobalsResp\"\x00\x42\"Z\x1dgithub.com/mailgun/gubernator\x80\x01\x01\x62\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'peers_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'Z\035github.com/mailgun/gubernator\200\001\001'
  _globals['_GETPEERRATELIMITSREQ']._serialized_start=48
  _globals['_GETPEERRATELIMITSREQ']._serialized_end=127
  _globals['_GETPEERRATELIMITSRESP']._serialized_start=129
  _globals['_GETPEERRATELIMITSRESP']._serialized_end=215
  _globals['_UPDATEPEERGLOBALSREQ']._serialized_start=217
  _globals['_UPDATEPEERGLOBALSREQ']._serialized_end=298
  _globals['_UPDATEPEERGLOBAL']._serialized_start=301
  _globals['_UPDATEPEERGLOBAL']._serialized_end=510
  _globals['_UPDATEPEERGLOBALSRESP']._serialized_start=512
  _globals['_UPDATEPEERGLOBALSRESP']._serialized_end=535
  _globals['_PEERSV1']._serialized_start=538
  _globals['_PEERSV1']._serialized_end=743
# @@protoc_insertion_point(module_scope)
