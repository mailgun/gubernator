# This code is py3.7 and py2.7 compatible

import gubernator.ratelimit_pb2_grpc as pb_grpc
from datetime import datetime

import time
import grpc

MILLISECOND = 1
SECOND = MILLISECOND * 1000
MINUTE = SECOND * 60


def sleep_until_reset(reset_time):
    now = datetime.now()
    time.sleep((reset_time - now).seconds)


def V1Client(endpoint='127.0.0.1:9090'):
    channel = grpc.insecure_channel(endpoint)
    return pb_grpc.RateLimitServiceV1Stub(channel)
