import gubernator.pb.ratelimit_pb2 as pb
import gubernator.pb.ratelimit_pb2_grpc as pb_grpc
import grpc


class Client(object):
    def __init__(self, endpoint='127.0.0.1:9090', timeout=None,
                 username=None, password=None):
        channel = grpc.insecure_channel(endpoint)
        self.stub = pb_grpc.RateLimitServiceStub(channel)

    def health_check(self):
        print(self.stub.HealthCheck(pb.HealthCheckRequest()))
