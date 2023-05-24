# Generated by the gRPC Python protocol compiler plugin. DO NOT EDIT!
"""Client and server classes corresponding to protobuf-defined services."""
import grpc

from gubernator import gubernator_pb2 as gubernator__pb2


class V1Stub(object):
    """Missing associated documentation comment in .proto file."""

    def __init__(self, channel):
        """Constructor.

        Args:
            channel: A grpc.Channel.
        """
        self.GetRateLimits = channel.unary_unary(
                '/pb.gubernator.V1/GetRateLimits',
                request_serializer=gubernator__pb2.GetRateLimitsReq.SerializeToString,
                response_deserializer=gubernator__pb2.GetRateLimitsResp.FromString,
                )
        self.HealthCheck = channel.unary_unary(
                '/pb.gubernator.V1/HealthCheck',
                request_serializer=gubernator__pb2.HealthCheckReq.SerializeToString,
                response_deserializer=gubernator__pb2.HealthCheckResp.FromString,
                )


class V1Servicer(object):
    """Missing associated documentation comment in .proto file."""

    def GetRateLimits(self, request, context):
        """Given a list of rate limit requests, return the rate limits of each.
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def HealthCheck(self, request, context):
        """This method is for round trip benchmarking and can be used by
        the client to determine connectivity to the server
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')


def add_V1Servicer_to_server(servicer, server):
    rpc_method_handlers = {
            'GetRateLimits': grpc.unary_unary_rpc_method_handler(
                    servicer.GetRateLimits,
                    request_deserializer=gubernator__pb2.GetRateLimitsReq.FromString,
                    response_serializer=gubernator__pb2.GetRateLimitsResp.SerializeToString,
            ),
            'HealthCheck': grpc.unary_unary_rpc_method_handler(
                    servicer.HealthCheck,
                    request_deserializer=gubernator__pb2.HealthCheckReq.FromString,
                    response_serializer=gubernator__pb2.HealthCheckResp.SerializeToString,
            ),
    }
    generic_handler = grpc.method_handlers_generic_handler(
            'pb.gubernator.V1', rpc_method_handlers)
    server.add_generic_rpc_handlers((generic_handler,))


 # This class is part of an EXPERIMENTAL API.
class V1(object):
    """Missing associated documentation comment in .proto file."""

    @staticmethod
    def GetRateLimits(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(request, target, '/pb.gubernator.V1/GetRateLimits',
            gubernator__pb2.GetRateLimitsReq.SerializeToString,
            gubernator__pb2.GetRateLimitsResp.FromString,
            options, channel_credentials,
            insecure, call_credentials, compression, wait_for_ready, timeout, metadata)

    @staticmethod
    def HealthCheck(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(request, target, '/pb.gubernator.V1/HealthCheck',
            gubernator__pb2.HealthCheckReq.SerializeToString,
            gubernator__pb2.HealthCheckResp.FromString,
            options, channel_credentials,
            insecure, call_credentials, compression, wait_for_ready, timeout, metadata)
