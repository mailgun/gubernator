# This code is py3.7 and py2.7 compatible

import gubernator.pb.ratelimit_pb2_grpc as pb_grpc
import gubernator.pb.ratelimit_pb2 as pb
from datetime import datetime

import time
import grpc

MILLISECOND = 1
SECOND = MILLISECOND * 1000
MINUTE = SECOND * 60


class RateLimit(object):
    def __init__(self, status="", reset_time=0, remaining=0, limit=0):
        self.status = status
        self.reset_time = datetime.fromtimestamp(reset_time/1000.0)
        self.remaining = remaining
        self.limit = limit

    def __str__(self):
        return "RateLimit(status={}, " + \
               "reset_time={}, " + \
               " remaining={}, " + \
               " limit={}".format(self.status, self.reset_time,
                                  self.remaining, self.limit)

    def sleep_until_reset(self):
        now = datetime.now()
        time.sleep((self.reset_time-now).seconds)


class Client(object):
    def __init__(self, endpoint='127.0.0.1:9090', timeout=None):
        channel = grpc.insecure_channel(endpoint)
        self.stub = pb_grpc.RateLimitServiceStub(channel)
        self.timeout = timeout

    def health_check(self):
        return self.stub.HealthCheck(pb.HealthCheckRequest(),
                                     timeout=self.timeout)

    def get_rate_limit(self, namespace, unique, limit, duration, hits=0,
                       algorithm=pb.RateLimitConfig.TOKEN_BUCKET):
        req = pb.RateLimitRequestList()
        rate_limit = req.rate_limits.add()

        rate_limit.namespace = namespace
        rate_limit.unique_key = unique
        rate_limit.hits = hits
        rate_limit.rate_limit_config.limit = limit
        rate_limit.rate_limit_config.algorithm = algorithm
        rate_limit.rate_limit_config.duration = duration

        resp = self.stub.GetRateLimits(req, timeout=self.timeout)
        rl = resp.rate_limits[0]
        return RateLimit(
            status=rl.status,
            reset_time=rl.reset_time,
            remaining=rl.limit_remaining,
            limit=rl.current_limit,
        )
